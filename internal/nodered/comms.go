package nodered

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Node-RED publishes runtime events — debug output, node status, notifications
// — only over the editor's WebSocket at /comms. There is no HTTP endpoint for
// them. Without this, a model can deploy a flow but never observe what it did,
// which breaks the build/verify/fix loop at the verify step.
//
// The protocol, as implemented by @node-red/editor-api:
//
//	client -> {"auth":"<token>"}        (only when adminAuth is enabled)
//	server -> {"auth":"ok"} | {"auth":"fail"}   (then closes, on fail)
//	client -> {"subscribe":"debug"}     (no acknowledgement is sent)
//	server -> [{"topic":"debug","data":{...}}, ...]
//
// Heartbeats arrive on the "hb" topic and carry no output.

const (
	// debugTopic is the /comms subscription that carries debug-node output.
	debugTopic = "debug"

	// DefaultDebugBufferSize bounds how many debug messages are retained.
	// ponytail: fixed size; make it configurable if anyone hits the ceiling.
	DefaultDebugBufferSize = 500

	// Reconnect backoff. Node-RED restarts are routine — a redeploy bounces
	// the runtime — so a dropped connection is expected, not exceptional.
	reconnectMinDelay = 1 * time.Second
	reconnectMaxDelay = 30 * time.Second
)

// DebugMessage is one entry as it appeared in the debug sidebar. Data is kept
// opaque: its shape is Node-RED's, varies by node, and grows between versions.
type DebugMessage struct {
	ReceivedAt time.Time       `json:"receivedAt"`
	Data       json.RawMessage `json:"data"`
}

// DebugSnapshot is a point-in-time view of the tail. It reports the connection
// state and the drop count alongside the messages, so a caller can tell "the
// flow produced nothing" from "we were not connected" or "the buffer overran".
type DebugSnapshot struct {
	Messages  []DebugMessage
	Buffered  int
	Received  int
	Dropped   int
	Connected bool
	LastError string
}

// DebugTail keeps a bounded, newest-wins buffer of debug output streamed from
// a Node-RED instance. It is safe for concurrent use.
type DebugTail struct {
	wsURL    string
	token    string
	insecure bool

	mu        sync.Mutex
	ring      []DebugMessage
	next      int
	filled    bool
	received  int
	connected bool
	lastErr   string
}

// NewDebugTail builds a tail for the instance the client is configured against.
// Call Run in a goroutine to start collecting.
func NewDebugTail(c *Client, capacity int) (*DebugTail, error) {
	wsURL, err := commsURL(c.baseURL)
	if err != nil {
		return nil, err
	}
	token := ""
	if ta, ok := c.auth.(*tokenAuth); ok {
		token = ta.token
	}
	return newDebugTail(wsURL, token, c.insecure, capacity), nil
}

func newDebugTail(wsURL, token string, insecure bool, capacity int) *DebugTail {
	if capacity <= 0 {
		capacity = DefaultDebugBufferSize
	}
	return &DebugTail{
		wsURL:    wsURL,
		token:    token,
		insecure: insecure,
		ring:     make([]DebugMessage, capacity),
	}
}

// commsURL derives the /comms WebSocket endpoint from the admin API base URL,
// preserving any path prefix Node-RED is mounted under.
func commsURL(baseURL string) (string, error) {
	if baseURL == "" {
		return "", errors.New("base URL is required")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("base URL must be http or https, got %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/comms"
	return u.String(), nil
}

// Run connects, subscribes, and streams debug messages into the buffer until
// ctx is cancelled. It never returns an error: an unreachable or restarting
// Node-RED is an expected condition, recorded in the snapshot and retried,
// not a reason to bring the MCP server down.
func (t *DebugTail) Run(ctx context.Context) {
	delay := reconnectMinDelay
	for {
		if err := t.session(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			t.setState(false, err)
			slog.Debug("debug tail disconnected, will retry", "error", err, "retry_in", delay)
		}
		if ctx.Err() != nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		// Exponential backoff, capped: a Node-RED that stays down must not be
		// hammered, but a quick redeploy should be picked up quickly.
		if delay *= 2; delay > reconnectMaxDelay {
			delay = reconnectMaxDelay
		}
	}
}

// session runs one connection from dial to disconnect.
func (t *DebugTail) session(ctx context.Context) error {
	opts := &websocket.DialOptions{}
	if t.insecure {
		opts.HTTPClient = &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // opt-in
		}}
	}

	conn, _, err := websocket.Dial(ctx, t.wsURL, opts)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", t.wsURL, err)
	}
	defer conn.CloseNow()
	// Debug payloads can be large; the default read limit is 32 KiB.
	conn.SetReadLimit(4 << 20)

	if t.token != "" {
		if err := t.authenticate(ctx, conn); err != nil {
			return err
		}
	}

	sub, err := json.Marshal(map[string]string{"subscribe": debugTopic})
	if err != nil {
		return fmt.Errorf("encoding subscription: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, sub); err != nil {
		return fmt.Errorf("subscribing: %w", err)
	}

	t.setState(true, nil)
	slog.Info("debug tail connected", "url", t.wsURL)
	defer t.setState(false, nil)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("reading: %w", err)
		}
		t.consume(data)
	}
}

// authenticate performs the {"auth":token} handshake Node-RED requires when
// adminAuth is enabled.
func (t *DebugTail) authenticate(ctx context.Context, conn *websocket.Conn) error {
	frame, err := json.Marshal(map[string]string{"auth": t.token})
	if err != nil {
		return fmt.Errorf("encoding auth: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
		return fmt.Errorf("sending auth: %w", err)
	}

	authCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, data, err := conn.Read(authCtx)
	if err != nil {
		return fmt.Errorf("awaiting auth reply: %w", err)
	}

	var reply struct {
		Auth string `json:"auth"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return fmt.Errorf("decoding auth reply: %w", err)
	}
	if reply.Auth != "ok" {
		// Retrying a rejected token forever would be pointless noise; the
		// backoff still caps it, and the reason is surfaced in the snapshot.
		return fmt.Errorf("authentication rejected by Node-RED (token invalid or lacking status.read)")
	}
	return nil
}

// consume decodes one /comms frame and records any debug messages it carries.
func (t *DebugTail) consume(frame []byte) {
	var envelopes []struct {
		Topic string          `json:"topic"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(frame, &envelopes); err != nil {
		// Not an array: an out-of-band object such as a late auth reply.
		// Ignoring it is correct — we only care about debug output.
		slog.Debug("ignoring unrecognised comms frame", "bytes", len(frame))
		return
	}
	for _, e := range envelopes {
		// Only debug output is retained. Node-RED multiplexes other topics
		// onto the same socket — "hb" heartbeats above all — and none of them
		// are things a debug node produced.
		if e.Topic != debugTopic {
			continue
		}
		t.record(e.Data)
	}
}

// record appends one message, overwriting the oldest once the ring is full.
func (t *DebugTail) record(data json.RawMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ring[t.next] = DebugMessage{ReceivedAt: time.Now(), Data: data}
	t.next = (t.next + 1) % len(t.ring)
	if t.next == 0 {
		t.filled = true
	}
	t.received++
}

func (t *DebugTail) setState(connected bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = connected
	if err != nil {
		t.lastErr = err.Error()
	} else if connected {
		t.lastErr = ""
	}
}

// Snapshot returns the buffered messages, oldest first, optionally limited to
// the newest limit entries and to those received after since.
func (t *DebugTail) Snapshot(limit int, since time.Time) DebugSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Walk the ring oldest-to-newest.
	var ordered []DebugMessage
	if t.filled {
		ordered = append(ordered, t.ring[t.next:]...)
	}
	ordered = append(ordered, t.ring[:t.next]...)

	snap := DebugSnapshot{
		Buffered:  len(ordered),
		Received:  t.received,
		Dropped:   t.received - len(ordered),
		Connected: t.connected,
		LastError: t.lastErr,
	}

	if !since.IsZero() {
		filtered := ordered[:0:0]
		for _, m := range ordered {
			if m.ReceivedAt.After(since) {
				filtered = append(filtered, m)
			}
		}
		ordered = filtered
	}
	// A limit keeps the newest: when debugging, the tail is what matters.
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[len(ordered)-limit:]
	}
	snap.Messages = ordered
	return snap
}

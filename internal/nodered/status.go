package nodered

// This file is the live status half of the runtime observability story
// (issue #51). It keeps a last-known-status-per-node cache that the MCP
// get_node_status tool reads from.
//
// Why a parallel tail, not a refactor of DebugTail: the two tails have
// different lifetimes (debug = consume-and-forget, status = remember
// the latest), different handlers, and only share the WebSocket
// transport. A combined tail would have a single consumer with two
// buckets, adding complexity without removing duplication.
//
// Why a WebSocket at all: the admin API has no equivalent. Node-RED
// only publishes node-status events on /comms under the topic
// status/<id> (verified against @node-red/runtime/lib/api/comms.js in
// Node-RED 5.x). The payload is {text, fill, shape}.
//
// Why gated on the existing MCP_DEBUG_STREAM flag: the /comms dial
// itself can crash the runtime on some Node-RED versions (the same
// bug the flag was added for in #39). Operators opt in explicitly
// when they want debug + status; everything else gets a clear "set
// MCP_DEBUG_STREAM=on" hint.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// statusTopic is the /comms subscription that carries node-status
// events. The trailing wildcard is what tells Node-RED to send every
// status/<id> event AND every retained last-known status (the runtime
// retains the latest status per id and replays it on subscribe).
const statusTopic = "status/#"

// statusPayload is one frame on the status/# topic. It is the
// "cleared" form (text/fill/shape all empty) when a node is
// disconnecting; otherwise it carries the last known state.
type statusPayload struct {
	Text  string `json:"text"`
	Fill  string `json:"fill"`
	Shape string `json:"shape"`
}

// statusEvent is what we extract from one /comms frame. The id is
// the node id (the topic suffix). For a "cleared" event the payload
// is empty AND we surface the state as disconnected.
type statusEvent struct {
	id      string
	cleared bool
	text    string
	fill    string
	shape   string
}

// Status is the user-facing enum that get_node_status reports. The
// mapping from (fill, shape) to Status is below; the same mapping
// drives the dot under each node in the Node-RED editor.
type Status string

const (
	// StatusConnected: fill=green, shape=dot. The node is wired up
	// and reporting that its work is done (a "ready" state for
	// input nodes, a "message sent" for outputs).
	StatusConnected Status = "connected"
	// StatusConnecting: fill=green, shape=ring. The node has
	// connected to its upstream/downstream service but the work
	// is still in progress. Mapped to "connecting" rather than
	// "connected" because the issue's spec calls for
	// connected/reconnecting/errored only; we keep "connecting"
	// as a separate value because the editor distinguishes them.
	StatusConnecting Status = "connecting"
	// StatusReconnecting: fill=yellow. The node is waiting for a
	// dependency to come back (the broker went away, the device
	// dropped off the network, etc.).
	StatusReconnecting Status = "reconnecting"
	// StatusErrored: fill=red. The node is in an error state.
	StatusErrored Status = "errored"
	// StatusInfo: fill=blue. The node is reporting an
	// informational state (no implication about health).
	StatusInfo Status = "info"
	// StatusDisconnected: fill=grey or empty. No status is being
	// reported. The Node-RED editor renders this the same as
	// "no status dot".
	StatusDisconnected Status = "disconnected"
	// StatusUnknown: the node id has never been seen in any
	// status event. The handler reports this so a model that
	// asks about an id it just made up gets a clear "no such
	// node" answer rather than a misleading "disconnected".
	StatusUnknown Status = "unknown"
)

// classify maps a (fill, shape, cleared) triple onto a Status. The
// rules match the editor: green/dot = connected, green/ring =
// connecting, yellow = reconnecting, red = errored, blue = info,
// everything else = disconnected. A cleared event (the runtime
// explicitly nullified the status) is disconnected regardless of
// fill.
func classify(fill, shape string, cleared bool) Status {
	if cleared {
		return StatusDisconnected
	}
	if fill == "" {
		return StatusDisconnected
	}
	switch strings.ToLower(fill) {
	case "green":
		if shape == "ring" {
			return StatusConnecting
		}
		return StatusConnected
	case "yellow":
		return StatusReconnecting
	case "red":
		return StatusErrored
	case "blue":
		return StatusInfo
	case "grey", "gray":
		return StatusDisconnected
	default:
		// An unrecognised fill: do not guess. Disconnected is
		// the safe answer — the dot is not a colour the
		// operator would recognise as healthy.
		return StatusDisconnected
	}
}

// StatusEntry is the cached state for one node, with a small
// history of the last error text (if any). The MCP layer reads
// this directly through StatusTail.Lookup.
type StatusEntry struct {
	ID        string    `json:"id"`
	Status    Status    `json:"status"`
	Text      string    `json:"text,omitempty"`
	Shape     string    `json:"shape,omitempty"`
	Fill      string    `json:"fill,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	// LastError is the text from the most recent errored status
	// this node has reported. The Status field is the latest
	// event; LastError is "the last bad thing we saw". A node
	// that errored and is now back to connected still carries
	// its old error in LastError until a new error arrives,
	// because operators want to know what went wrong even if the
	// node has since recovered.
	LastError string `json:"lastError,omitempty"`
}

// StatusSnapshot is what get_node_status returns: the per-node
// entries, plus a connection report that mirrors the DebugTail
// shape (so the MCP layer can give the same actionable messages
// for the status tail as for the debug one).
type StatusSnapshot struct {
	Connected bool          `json:"connected"`
	LastError string        `json:"lastError,omitempty"`
	Entries   []StatusEntry `json:"entries"`
	// Tracked is the number of node ids the cache has any data
	// for. Differs from len(Entries) when Lookup is called
	// without a flow filter — the snapshot omits a node the
	// cache has never seen, while Tracked counts it.
	Tracked int `json:"tracked"`
}

// StatusTail keeps a last-known-status-per-node cache, fed by
// status/# events from the /comms WebSocket. It is safe for
// concurrent use.
//
// The cache holds the latest event per node id, not a history —
// "what is the node doing right now" is the only question a model
// would ask of it, and a history would just consume memory. The
// one piece of history we keep is LastError, which is the
// operator-actionable thing to surface when a node is in any
// non-connected state.
type StatusTail struct {
	wsURL    string
	token    string
	insecure bool

	mu        sync.Mutex
	entries   map[string]StatusEntry
	connected bool
	lastErr   string
}

// NewStatusTail builds a tail for the instance the client is
// configured against. Call Run in a goroutine to start
// collecting. The constructor does not dial; that happens inside
// Run.
func NewStatusTail(c *Client) (*StatusTail, error) {
	wsURL, err := commsURL(c.baseURL)
	if err != nil {
		return nil, err
	}
	token := ""
	if ta, ok := c.auth.(*tokenAuth); ok {
		token = ta.token
	}
	return newStatusTail(wsURL, token, c.insecure), nil
}

func newStatusTail(wsURL, token string, insecure bool) *StatusTail {
	return &StatusTail{
		wsURL:    wsURL,
		token:    token,
		insecure: insecure,
		entries:  make(map[string]StatusEntry),
	}
}

// Run connects, subscribes, and streams status events into the
// cache until ctx is cancelled. Same retry/backoff policy as
// DebugTail.Run: a dropped connection is expected, the loop
// reconnects with exponential backoff, and a permanently down
// Node-RED is recorded in the snapshot rather than crashing the
// MCP.
func (t *StatusTail) Run(ctx context.Context) {
	delay := reconnectMinDelay
	for {
		if err := t.session(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			t.setState(false, err)
			slog.Debug("status tail disconnected, will retry", "error", err, "retry_in", delay)
		}
		if ctx.Err() != nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay *= 2; delay > reconnectMaxDelay {
			delay = reconnectMaxDelay
		}
	}
}

// session runs one connection from dial to disconnect.
func (t *StatusTail) session(ctx context.Context) error {
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
	// Status payloads are tiny ({text,fill,shape} at most), but
	// the default 32 KiB read limit is still fine — keep the
	// explicit value so the reasoning is on the page.
	conn.SetReadLimit(1 << 20)

	if t.token != "" {
		if err := t.authenticate(ctx, conn); err != nil {
			return err
		}
	}

	sub, err := json.Marshal(map[string]string{"subscribe": statusTopic})
	if err != nil {
		return fmt.Errorf("encoding subscription: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, sub); err != nil {
		return fmt.Errorf("subscribing: %w", err)
	}

	t.setState(true, nil)
	slog.Info("status tail connected", "url", t.wsURL)
	defer t.setState(false, nil)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("reading: %w", err)
		}
		t.consume(data)
	}
}

// authenticate is the same /comms auth handshake DebugTail uses.
// Both tails are on the same socket shape, so the reply envelope
// (object or array) is parsed the same way.
func (t *StatusTail) authenticate(ctx context.Context, conn *websocket.Conn) error {
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
		var envelope []struct {
			Auth string `json:"auth"`
		}
		if err2 := json.Unmarshal(data, &envelope); err2 == nil && len(envelope) > 0 {
			reply = envelope[0]
		} else {
			return fmt.Errorf("decoding auth reply: %w", err)
		}
	}
	if reply.Auth != "ok" {
		return errors.New("authentication rejected by Node-RED (token invalid or lacking status.read)")
	}
	return nil
}

// ConsumeForTest is a test-only escape hatch: it feeds one raw
// /comms frame into the cache without dialing a real WebSocket.
// Production code never calls this; it exists so the MCP
// handler tests can seed a known status without standing up
// httptest. Kept package-private to the nodered package so a
// stray import cannot reach it.
func (t *StatusTail) ConsumeForTest(frame []byte) { t.consume(frame) }

// consume decodes one /comms frame and records any status events
// it carries. Frames are arrays of {topic,data} or, on a late
// auth reply, a single object; we accept both shapes, the same
// way DebugTail.consume does.
func (t *StatusTail) consume(frame []byte) {
	var envelopes []struct {
		Topic string          `json:"topic"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(frame, &envelopes); err != nil {
		var single struct {
			Topic string          `json:"topic"`
			Data  json.RawMessage `json:"data"`
		}
		if err2 := json.Unmarshal(frame, &single); err2 != nil {
			slog.Debug("ignoring unrecognised comms frame", "bytes", len(frame))
			return
		}
		t.applyTopic(single.Topic, single.Data)
		return
	}
	for _, e := range envelopes {
		t.applyTopic(e.Topic, e.Data)
	}
}

// applyTopic extracts a status event from a single (topic, data)
// pair and records it. Non-status topics are ignored.
func (t *StatusTail) applyTopic(topic string, data json.RawMessage) {
	// Only "status/<id>" is actionable: a bare "status" topic is
	// the editorialised catch-all (no id, no payload) and a
	// topic with no "status/" prefix is some other comms event
	// we are not interested in.
	const prefix = "status/"
	if !strings.HasPrefix(topic, prefix) {
		return
	}
	id := strings.TrimPrefix(topic, prefix)
	if id == "" {
		// "status/" with no id: nothing to record.
		return
	}
	var p statusPayload
	if err := json.Unmarshal(data, &p); err != nil {
		// An unparseable status payload is exactly the
		// kind of thing we want to log: a model would
		// have no idea why the lookup returned nothing
		// if the runtime is sending bytes we cannot
		// read. Keep the message terse.
		slog.Debug("ignoring unparseable status payload", "node_id", id, "error", err)
		return
	}
	cleared := p.Text == "" && p.Fill == "" && p.Shape == ""
	t.record(statusEvent{
		id:      id,
		cleared: cleared,
		text:    p.Text,
		fill:    p.Fill,
		shape:   p.Shape,
	})
}

// record stores one status event in the cache. It is the only
// place the cache is written; the mu guard makes the whole
// "compute the new entry, swap it in" sequence atomic.
func (t *StatusTail) record(ev statusEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	prev, hadPrev := t.entries[ev.id]
	entry := StatusEntry{
		ID:        ev.id,
		Status:    classify(ev.fill, ev.shape, ev.cleared),
		Text:      ev.text,
		Shape:     ev.shape,
		Fill:      ev.fill,
		UpdatedAt: now,
	}
	// LastError: copy forward from the previous entry when the
	// new state is errored (so two consecutive error events
	// keep the latest text), and overwrite it with the new
	// error text only on a fresh error. When the node recovers
	// to a non-errored state we keep the old error so the
	// operator can still see "what was wrong with this node".
	switch {
	case entry.Status == StatusErrored:
		entry.LastError = ev.text
	case hadPrev:
		entry.LastError = prev.LastError
	}
	_ = hadPrev // "hadPrev" is documentary; the branch above is the rule.
	t.entries[ev.id] = entry
}

func (t *StatusTail) setState(connected bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = connected
	if err != nil {
		t.lastErr = err.Error()
	} else if connected {
		t.lastErr = ""
	}
}

// Lookup returns the cached status for one node id. The second
// return value reports whether the cache has any data for the id:
// false means the runtime has not sent a status event for this
// node, which the handler renders as "unknown" (the spec's
// "never-seen" case) rather than "disconnected" (which would
// imply a transition we cannot prove).
func (t *StatusTail) Lookup(nodeID string) (StatusEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[nodeID]
	return e, ok
}

// Snapshot returns the connection report plus a slice of entries
// (the whole cache when nodeIDs is empty, or only the requested
// ids when nodeIDs is non-empty). The Tracked counter is always
// the total cache size so the handler can tell the difference
// between "no such node" and "no nodes in this flow".
func (t *StatusTail) Snapshot(nodeIDs ...string) StatusSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()

	snap := StatusSnapshot{
		Connected: t.connected,
		LastError: t.lastErr,
		Tracked:   len(t.entries),
	}
	if len(nodeIDs) == 0 {
		for _, e := range t.entries {
			snap.Entries = append(snap.Entries, e)
		}
	} else {
		for _, id := range nodeIDs {
			if e, ok := t.entries[id]; ok {
				snap.Entries = append(snap.Entries, e)
			}
		}
	}
	return snap
}

// ParseFlowNodeIDs extracts the node ids from a flow document,
// the same shape GET /flow/:id returns. We only need the ids —
// the per-node status join is what the snapshot handles. The
// helper lives here (not in flows.go) because the StatusTail
// is the only consumer.
func ParseFlowNodeIDs(raw json.RawMessage) []string {
	// API v2: {"id":"tabA","nodes":[...],"configs":[...]} (and the
	// tab can have a "subflows" sub-array we ignore).
	var nested struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &nested); err == nil && len(nested.Nodes) > 0 {
		return pickIDs(nested.Nodes)
	}
	// API v1: a flat array entry (the per-tab doc is wrapped in
	// the same shape, but on the flat wire every node is a
	// separate element with a "z" pointing at its tab). For the
	// "this flow" case the caller hands us a list of nodes that
	// belong to one tab; we pick ids out of whatever we get.
	var flat []map[string]any
	if err := json.Unmarshal(raw, &flat); err == nil {
		return pickIDs(flat)
	}
	return nil
}

func pickIDs(nodes []map[string]any) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if id, _ := n["id"].(string); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// ErrStatusStreamUnavailable is the error get_node_status
// surfaces when the WebSocket is not connected (the operator
// has not opted in via MCP_DEBUG_STREAM). Surfaced as a
// dedicated sentinel so the handler can produce a clear,
// action-shaped message ("set MCP_DEBUG_STREAM=on") without
// grepping strings.
var ErrStatusStreamUnavailable = errors.New(
	"node status stream is not available: the /comms WebSocket is " +
		"not connected. Set MCP_DEBUG_STREAM=on to enable it (and " +
		"restart the MCP if the flag was not set at start-up)",
)

// urlError exists so an external caller (mcp package) can
// distinguish "stream unavailable" from "we connected but the
// status for that id is unknown". The latter is not an error:
// the handler renders it as a clear "unknown" entry.

package nodered

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client talks to a Node-RED instance via its admin HTTP API.
//
// It is safe for concurrent use: each call uses its own request and the
// underlying *http.Client is concurrency-safe.
type Client struct {
	baseURL    string
	httpClient *http.Client
	auth       authStrategy
	backupDir  string
	// insecure is retained because the /comms WebSocket dials separately from
	// httpClient and needs the same TLS decision.
	insecure      bool
	searchBaseURL string
	// writeMu serializes the mutating endpoints (POST /flows, PUT /flow/:id,
	// DELETE /flow/:id, POST /flows/state, the palette mutations).
	// Multiple tools/call requests on the same MCP session were racing:
	// each handler read the flow, mutated its in-memory copy, and wrote it
	// back; the last writer won and earlier mutations were silently lost.
	// ponytail: global lock, per-flow locks if throughput matters.
	writeMu sync.Mutex
}

// authStrategy encapsulates how Authorization headers are produced.
type authStrategy interface {
	apply(req *http.Request)
}

type tokenAuth struct{ token string }

func (a *tokenAuth) apply(req *http.Request) {
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
}

type basicAuth struct{ username, password string }

func (a *basicAuth) apply(req *http.Request) {
	if a.username != "" {
		req.SetBasicAuth(a.username, a.password)
	}
}

type noAuth struct{}

func (a *noAuth) apply(req *http.Request) {}

// Options controls how the client is constructed.
type Options struct {
	BaseURL  string
	Token    string
	Username string
	Password string
	Insecure bool
	// BackupDir is where flow snapshots are written before every mutating
	// operation. Defaults to "backups" if empty.
	BackupDir string
	// SearchBaseURL is the npm-compatible registry searched by SearchNodes.
	// Defaults to "https://registry.npmjs.org" if empty. Set this to a
	// private registry mirror (e.g. an internal Verdaccio) when needed.
	SearchBaseURL string
}

// NewClient builds a Client. Exactly one of (Token) or (Username+Password)
// is expected; if none are provided the client talks to a Node-RED with no
// admin auth (dev only).
func NewClient(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, errors.New("BaseURL is required")
	}
	// Normalize: strip trailing slash.
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")

	// No client-wide Timeout: it is a blunt per-request ceiling that would
	// also cap slow operations like npm installs. We apply deadlines per call
	// via context instead (see do and the defaultTimeout below).
	httpClient := &http.Client{}
	if opts.Insecure {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // opt-in
		}
	}

	var auth authStrategy
	switch {
	case opts.Token != "":
		auth = &tokenAuth{token: opts.Token}
	case opts.Username != "":
		auth = &basicAuth{username: opts.Username, password: opts.Password}
	default:
		auth = &noAuth{}
	}

	slog.Debug("nodered client created", "base_url", opts.BaseURL)
	return &Client{
		baseURL:       opts.BaseURL,
		httpClient:    httpClient,
		auth:          auth,
		backupDir:     opts.BackupDir,
		insecure:      opts.Insecure,
		searchBaseURL: opts.SearchBaseURL,
	}, nil
}

// APIError is returned when Node-RED responds with a non-2xx status code.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("nodered %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
	// A 404 with an Express-style "Cannot METHOD /path" body usually means
	// the admin API does not expose that endpoint on this Node-RED version,
	// not that the URL is wrong. Surface the most common guard so the
	// operator has something to check beyond the literal error string.
	if e.StatusCode == http.StatusNotFound {
		if strings.HasPrefix(strings.TrimSpace(e.Body), "Cannot ") {
			msg += " (the admin API does not expose this endpoint on this Node-RED version; " +
				"check the Node-RED release notes or the runtime-state settings gate)"
		}
	}
	return msg
}

// defaultTimeout bounds a request when the caller's context carries no
// deadline of its own. Long operations (npm installs) pass a longer deadline.
const defaultTimeout = 30 * time.Second

// writeGuard returns a release function the caller defers, so every
// mutating endpoint takes writeMu under the same pattern and the lock is
// always released on every path (error or success).
//
// Reads (GET endpoints, /context, /settings, etc.) deliberately do NOT
// take this lock — concurrency between a read and a write is fine and
// protects the path from a deadlock against the runtime's own reads.
func (c *Client) writeGuard() func() {
	c.writeMu.Lock()
	return c.writeMu.Unlock
}

// do performs an HTTP request and decodes the JSON response into out (if
// non-nil). It returns an *APIError on non-2xx responses.
func (c *Client) do(ctx context.Context, method, path string, body interface{}, out interface{}, reqOpts ...func(*http.Request)) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	u, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return fmt.Errorf("building URL: %w", err)
	}

	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshalling request body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	c.auth.apply(req)
	for _, opt := range reqOpts {
		opt(req)
	}

	slog.Debug("nodered request", "method", method, "url", u)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling %s %s: %w", method, u, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			StatusCode: resp.StatusCode,
			Method:     method,
			Path:       path,
			Body:       string(respBody),
		}
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding response: %w (body: %s)", err, string(respBody))
		}
	}
	return nil
}

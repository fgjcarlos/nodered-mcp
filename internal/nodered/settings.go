package nodered

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// settingsStateTimeout bounds pause/resume (POST /flows/state) and full
// deploys (POST /flows): they can stall a busy runtime for a few seconds,
// longer than the read default of 30s. The 2-minute ceiling covers a slow
// stop on a large flow without leaving the user waiting on a hung call.
const settingsStateTimeout = 2 * time.Minute

// searchTimeout bounds the npm registry search. The public endpoint is
// fast (<1s) but we leave room for a slow network.
const searchTimeout = 10 * time.Second

// Settings is a read-only view of the Node-RED server configuration, as
// returned by GET /settings. Node-RED returns a large object; we surface it
// as opaque JSON so the LLM can read whatever it needs (adminAuth scheme,
// port, https, editor theme, etc.) without us cherry-picking fields.
type Settings = json.RawMessage

// FlowsState is the runtime state of a Node-RED instance (started/stopped),
// as returned by GET /flows/state. The exact shape is owned by Node-RED; we
// keep it opaque.
type FlowsState = json.RawMessage

// GetSettings returns the Node-RED server settings as opaque JSON. Read-only.
func (c *Client) GetSettings(ctx context.Context) (Settings, error) {
	var raw Settings
	if err := c.do(ctx, "GET", "/settings", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// GetFlowsState returns the current runtime state of Node-RED
// (started/stopped, plus a per-flow breakdown in recent versions). Read-only.
func (c *Client) GetFlowsState(ctx context.Context) (FlowsState, error) {
	var raw FlowsState
	if err := c.do(ctx, "GET", "/flows/state", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// SetFlowsState starts or stops the Node-RED runtime (POST /flows/state).
// MUTATES the runtime. A backup of the current flow config is taken first
// so the change can be rolled back.
func (c *Client) SetFlowsState(ctx context.Context, state string) error {
	defer c.writeGuard()()
	switch state {
	case "start", "stop":
		// ok
	default:
		return fmt.Errorf("state must be \"start\" or \"stop\", got %q", state)
	}
	ctx, cancel := context.WithTimeout(ctx, settingsStateTimeout)
	defer cancel()

	if _, err := c.snapshotFlows(ctx); err != nil {
		return err
	}
	return c.do(ctx, "POST", "/flows/state", map[string]string{"state": state}, nil)
}

// SetFlows replaces the ENTIRE flow config with the supplied flow array
// (POST /flows as a full deployment). This is the most destructive operation
// the admin API exposes — a backup of the current config is taken first so
// the change can be rolled back, and the {rev,...} envelope (if any) is
// stripped so the deploy never carries a stale rev.
func (c *Client) SetFlows(ctx context.Context, flows []json.RawMessage) error {
	return c.setFlows(ctx, flows, true)
}

// SetFlowsUnchecked deploys the configuration without validating its
// references.
//
// It is the recovery path for a configuration the guard refuses: the
// rules the guard applies are the repository's, and one of them (a wire
// that leaves the tab that owns it) is stricter than the runtime, which
// does resolve such a wire across tabs. An operator who wants that
// configuration installed has to ask for it explicitly — nothing reaches
// this method by accident — and the deployment still carries the usual
// snapshot, so the state being replaced can be recovered.
//
// The node-type policy and the tab rule are not skipped: they are not
// about the references of the configuration but about what a deployment
// may do to the runtime.
func (c *Client) SetFlowsUnchecked(ctx context.Context, flows []json.RawMessage) error {
	return c.setFlows(ctx, flows, false)
}

// setFlows deploys a flow array as a full configuration. When validate is
// false the references of the configuration are not checked.
func (c *Client) setFlows(ctx context.Context, flows []json.RawMessage, validate bool) error {
	defer c.writeGuard()()
	if len(flows) == 0 {
		return errors.New("flows: at least one element is required")
	}
	// A full deployment replaces the whole configuration, so the policy is
	// judged on the array before anything is snapshotted or sent.
	if err := c.deniedNodeTypeInFlows(flows); err != nil {
		return err
	}
	// Issue #106: Node-RED accepts a non-empty flows array that contains
	// zero tab entries (e.g. one orphan inject node) and deploys it,
	// leaving the runtime with no tabs and a node that has nowhere to
	// live. Reject the input before taking a backup or contacting the
	// runtime so the user's existing tab structure is preserved.
	//
	// The entries are read, not searched for: the first version of this
	// check looked for the bytes `"type":"tab"`, which refused a payload
	// whose JSON was formatted with a space after the colon — and the
	// refusal named the configuration rather than the formatting.
	if !hasTabEntry(flows) {
		return errors.New(`flows: array must contain at least one element with type "tab"; received only non-tab entries`)
	}
	// Issue #334: a full deployment replaces the whole configuration, so
	// the references of the configuration are checked before anything is
	// snapshotted and before the runtime is contacted. A wire that names
	// a node which is not in the same tab, a node whose z names no tab at
	// all, and an instance of a subflow whose definition is missing are
	// all refused here instead of being installed.
	if validate {
		if err := validateEffectiveConfig(flows); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, settingsStateTimeout)
	defer cancel()

	body, err := json.Marshal(flows)
	if err != nil {
		return fmt.Errorf("encoding flows: %w", err)
	}
	if _, err := c.snapshotFlows(ctx); err != nil {
		return err
	}
	return c.do(ctx, "POST", "/flows", json.RawMessage(body), nil, func(r *http.Request) {
		r.Header.Set("Node-RED-Deployment-Type", "full")
	})
}

// hasTabEntry reports whether the array carries an entry of type "tab".
func hasTabEntry(flows []json.RawMessage) bool {
	for _, f := range flows {
		var meta struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(f, &meta); err == nil && meta.Type == "tab" {
			return true
		}
	}
	return false
}

// SearchResult is one hit from the public npm registry search.
type SearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Date        string `json:"date,omitempty"`
	Link        string `json:"link,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
}

// SearchNodes queries the public npm registry for node-red-* modules.
// It uses the registry search endpoint scoped to the "node-red" keyword,
// which is what the public flow library (flows.nodered.org) is built on.
//
// It does not call the Node-RED admin API — it talks to npm directly. Read-only.
func (c *Client) SearchNodes(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if query == "" {
		return nil, errors.New("query is required")
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	// ponytail: registry URL defaults to the public registry; configurable
	// for private mirrors (see Options.SearchBaseURL).
	base := c.searchBaseURL
	if base == "" {
		base = "https://registry.npmjs.org"
	}
	u := fmt.Sprintf("%s/-/v1/search?text=keywords:node-red+%s&size=%d",
		strings.TrimRight(base, "/"), url.QueryEscape(query), limit)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("building search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	// Use the dedicated registry client: it always verifies TLS, independent
	// of NODERED_INSECURE. Reusing httpClient here would propagate the
	// admin-side InsecureSkipVerify to the public registry.
	resp, err := c.registryClient.Do(req)
	if err != nil {
		// Strip any query string (may contain tokens) before reporting the URL.
		redactedBase := strings.SplitN(base, "?", 2)[0]
		return nil, &TransportError{Err: fmt.Errorf("cannot reach npm registry at %s (check network connectivity): %w", redactedBase, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("npm registry search: HTTP %d", resp.StatusCode)
	}

	// The registry response has a heavy "objects" wrapper; we only need the
	// package metadata, so decode straight into a flat slice.
	var raw struct {
		Objects []struct {
			Package struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Version     string `json:"version"`
				Date        string `json:"date"`
				Links       struct {
					Homepage string `json:"homepage"`
					NPM      string `json:"npm"`
				} `json:"links"`
				Publisher struct {
					Username string `json:"username"`
				} `json:"publisher"`
			} `json:"package"`
		} `json:"objects"`
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &TransportError{Err: fmt.Errorf("decoding registry response: %w", err)}
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding registry response: %w", err)
	}

	out := make([]SearchResult, 0, len(raw.Objects))
	for _, o := range raw.Objects {
		p := o.Package
		// No name-prefix filter: the registry endpoint above already scopes
		// the search to packages tagged with the "node-red" keyword. An
		// additional HasPrefix("node-red") guard here would drop legitimate
		// scoped packages like @flowfuse/node-red-dashboard (the official
		// Node-RED Dashboard 2.0) for no benefit.
		link := p.Links.NPM
		if link == "" {
			link = p.Links.Homepage
		}
		out = append(out, SearchResult{
			Name:        p.Name,
			Description: p.Description,
			Version:     p.Version,
			Date:        p.Date,
			Link:        link,
			Publisher:   p.Publisher.Username,
		})
	}
	return out, nil
}

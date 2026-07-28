package nodered

// This file covers the runtime-log endpoint that the audit (#51) called
// for as a way to read what Node-RED printed: boot messages, deploy
// errors, module load failures. As of Node-RED 5.x there is no /logs
// admin endpoint by default — the runtime log is written to stdout and
// per-node log events stream on the /comms WebSocket under the
// event-log/<id> topic. The endpoint may be present on future Node-RED
// versions, on forks, or behind a logging plugin mounted at /logs; this
// client calls it the standard way and lets the handler surface 404 to
// the operator.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LogEntry is one normalised line from the runtime log. The wire shape
// varies by runtime version and by the plugin that mounted /logs (if
// any); we coerce everything we see into a small, common struct so the
// MCP layer can filter without knowing which variant is in play.
//
// Fields:
//
//   - Timestamp is the moment the runtime emitted the line, when the
//     body carries it. Zero value means the runtime did not give us a
//     time (we still surface the line — better than dropping it).
//   - Level is one of "info", "warn", "error" when the body names one.
//     Empty means the body did not classify the line.
//   - Message is the human-readable text. Always set.
type LogEntry struct {
	Timestamp time.Time `json:"ts,omitempty"`
	Level     string    `json:"level,omitempty"`
	Message   string    `json:"msg"`
}

// Logs is the parsed response of GET /logs: zero or more entries in the
// order they appeared on the wire. An empty slice means the runtime
// returned a body we could not classify as log content; the MCP layer
// decides how to surface that (currently: empty result, no error).
type Logs []LogEntry

// GetRuntimeLogs fetches the most recent runtime log lines from the
// connected Node-RED instance. count is the maximum number of lines to
// request; the runtime is free to return fewer (and usually does).
//
// The endpoint is /logs and is not part of the standard Node-RED admin
// API as of 5.x — a 404 here is the expected answer on a stock runtime
// and the handler translates it to an actionable operator message. On a
// runtime that does expose /logs, the response shape is treated
// opaquely: envelope ({logs:[...]}), bare array, or plain text (one line
// per \n).
//
// Read-only: this never mutates the runtime, and no backup is taken
// (there is nothing to roll back).
func (c *Client) GetRuntimeLogs(ctx context.Context, count int) (Logs, error) {
	if count <= 0 {
		count = 100
	}
	// Cap the request to a sane upper bound so a runaway caller cannot
	// ask the runtime for the whole history.
	if count > 1000 {
		count = 1000
	}

	// The body may be JSON, an array, or plain text; we cannot
	// route it through client.do because do() tries to JSON-decode
	// any non-empty body. We need the raw bytes.
	body, err := c.getRaw(ctx, "/logs?"+url.Values{"count": {strconv.Itoa(count)}}.Encode())
	if err != nil {
		return nil, err
	}
	return parseRuntimeLogs(body), nil
}

// parseRuntimeLogs accepts any of the shapes we have seen a /logs
// endpoint return, plus plain text (one line per \n). It is the
// permissive cousin of the existing endpoint parsers: the runtime owns
// the schema, the operator will tell us which variant is mounted, and
// either way we want a sensible Logs slice out of the bytes.
//
// A 404 is NOT handled here — the client.do path already wraps it in
// an *APIError and the handler renders a clear operator hint.
func parseRuntimeLogs(body []byte) Logs {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}
	// Plain text body (one line per \n). Anything that does not start
	// with { or [ falls through to this path: a JSON-decoder error on
	// the body of a /logs call is more confusing than a plain-text
	// fallback.
	if body[0] != '{' && body[0] != '[' {
		return parseLogsText(body)
	}

	// Envelope: {"logs": [...]}. This is the shape the v2 admin API
	// would naturally use and the most common one to land.
	var envelope struct {
		Logs []LogEntry `json:"logs"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Logs != nil {
		return normaliseLogs(envelope.Logs)
	}

	// Bare array of entries.
	var entries []LogEntry
	if err := json.Unmarshal(body, &entries); err == nil {
		return normaliseLogs(entries)
	}

	// Bare array of maps: tolerate the schema we cannot predict.
	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err == nil {
		return normaliseLogMaps(raw)
	}

	// Last resort: the body was JSON-shaped but not log-shaped. Treat
	// it as a single message rather than failing the call — the
	// operator asked for logs and this is what the runtime gave us.
	return parseLogsText(body)
}

// parseLogsText splits a plain-text body into one LogEntry per line.
// Whitespace-only lines are dropped; nothing else is filtered. The
// timestamp is left zero (the line did not carry one); the level is
// left empty (the line did not classify itself).
func parseLogsText(body []byte) Logs {
	var out Logs
	sc := bufio.NewScanner(bytes.NewReader(body))
	// A single log line can exceed the default 64 KiB scanner
	// buffer (a stack trace or a JSON dump pasted in by a custom
	// logger). Raise it to 1 MiB which is well above anything
	// sane and still bounded.
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, LogEntry{Message: line})
	}
	return out
}

// normaliseLogs returns its input with the Level field mapped onto the
// {"info","warn","error"} vocabulary the MCP layer exposes. An entry
// that already has a recognised level passes through unchanged;
// anything else is left as-is (empty, "debug", "trace", or arbitrary
// text).
func normaliseLogs(in []LogEntry) Logs {
	out := make(Logs, len(in))
	for i, e := range in {
		e.Level = normaliseLogLevel(e.Level)
		out[i] = e
	}
	return out
}

// normaliseLogMaps is the schema-free variant of normaliseLogs: a list
// of maps whose keys we do not know. We look for the usual suspects
// (timestamp/ts/time, level/level, message/msg/text) and pull them
// out. Anything we cannot find is left as the zero value.
func normaliseLogMaps(in []map[string]any) Logs {
	out := make(Logs, 0, len(in))
	for _, m := range in {
		e := LogEntry{
			Level:   pickString(m, "level", "severity"),
			Message: pickString(m, "msg", "message", "text"),
		}
		if ts := pickTime(m, "timestamp", "ts", "time"); !ts.IsZero() {
			e.Timestamp = ts
		}
		e.Level = normaliseLogLevel(e.Level)
		out = append(out, e)
	}
	return out
}

// pickString returns the first non-empty string value among the named
// keys. Empty values are skipped, so a present-but-empty key is
// treated as missing.
func pickString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// pickTime tries to coerce the value at the named key to a time.Time.
// It accepts RFC 3339 strings and epoch milliseconds (the two shapes
// we have actually seen). A failure on one key falls through to the
// next; a failure on all keys returns the zero time.
func pickTime(m map[string]any, keys ...string) time.Time {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		if t, ok := v.(time.Time); ok {
			return t
		}
		if s, ok := v.(string); ok && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t
			}
			// Try without nanoseconds (some custom loggers
			// emit this).
			if t, err := time.Parse("2006-01-02T15:04:05Z07:00", s); err == nil {
				return t
			}
		}
		if f, ok := v.(float64); ok {
			return time.UnixMilli(int64(f))
		}
	}
	return time.Time{}
}

// normaliseLogLevel maps the various level strings a runtime or a
// logging plugin can emit onto the {"info","warn","error"} vocabulary
// the MCP layer exposes. The mapping is intentionally lossy: "fatal"
// becomes "error", "trace" / "debug" become "info" (the public surface
// does not expose them).
func normaliseLogLevel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "info", "information", "notice":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error", "err", "fatal", "critical", "crit":
		return "error"
	default:
		return s
	}
}

// IsLogsNotFound reports whether an error from GetRuntimeLogs is the
// "endpoint not exposed on this runtime" case the handler wants to
// translate into a clear operator hint. Mirrors the APIError 404 gate
// get_flows_state uses for runtimeState.
func IsLogsNotFound(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

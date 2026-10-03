package nodered

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Version is a parsed Node-RED semver triple. Pre-release tags are
// dropped — the QA comparison only cares about the A.B.C numbers.
//
// The zero value is treated as "unknown" by AtLeast (returns
// false), which matches the cached value of an unprobed client.
type Version struct {
	Major int
	Minor int
	Patch int
	Known bool // false until a probe succeeds
	Raw   string
}

// AtLeast reports whether v is at or above the given triple. An
// unknown version (Known == false) returns false so callers must
// distinguish "older than this" from "could not tell".
func (v Version) AtLeast(maj, min, pat int) bool {
	if !v.Known {
		return false
	}
	if v.Major != maj {
		return v.Major > maj
	}
	if v.Minor != min {
		return v.Minor > min
	}
	return v.Patch >= pat
}

// String formats the version the way Node-RED itself does.
func (v Version) String() string {
	if v.Raw != "" {
		return v.Raw
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// ParseVersion parses the leading A.B.C from a Node-RED version
// string. Pre-release tags and build metadata are dropped. An
// empty string parses to a zero-value Version (Known == false).
func ParseVersion(s string) Version {
	s = strings.TrimSpace(s)
	if s == "" {
		return Version{}
	}
	parts := strings.SplitN(s, "-", 2)[0]    // drop "-rc1", "-beta", etc.
	parts = strings.SplitN(parts, "+", 2)[0] // drop build metadata
	nums := strings.SplitN(parts, ".", 3)
	if len(nums) == 0 || nums[0] == "" {
		return Version{}
	}
	v := Version{Raw: s}
	for i, slot := range []*int{&v.Major, &v.Minor, &v.Patch} {
		if i >= len(nums) {
			break
		}
		n, err := strconv.Atoi(nums[i])
		if err != nil {
			// Trailing "5" instead of "5.0.0" is fine; a non-numeric
			// component mid-string is not. Treat the whole value as
			// unparseable rather than silently zeroing a field.
			if i == 0 {
				return Version{}
			}
			break
		}
		*slot = n
	}
	v.Known = true
	return v
}

// versionCache is a one-shot probe wrapper: the first call to
// NodeRedVersion probes the runtime, every later call returns the
// cached value. sync.Once guarantees the probe runs at most once
// even with concurrent callers (a plain check-then-set would race:
// all goroutines see "not probed" and all probe).
//
// value and probed are read by CachedNodeRedVersion from a request
// handler, which does not take the Once. probed is therefore atomic;
// a plain bool would race with the closure that sets it. The value is
// stored BEFORE probed, so a reader that observes probed==true also
// observes the value — that ordering is what makes reading value
// without the Once safe.
//
// probed distinguishes "the probe ran, here is the result"
// (known-supported or known-too-low) from "nobody ever asked the
// runtime yet". Callers that want a non-probing read use
// CachedNodeRedVersion; they get the zero Version on the cold
// side, never a surprise network call mid-request.
type versionCache struct {
	once sync.Once
	// value is a plain field, not atomic. Every read of it is either
	// after once.Do returns (which sync.Once already orders) or after
	// probed.Load() returns true — and probed is atomic, so the
	// value-then-probed store order makes that read safe. A
	// non-atomic probed would race here: the handler reads the
	// cache without taking the Once.
	value  Version
	probed atomic.Bool
}

// NodeRedVersion returns the cached Node-RED version. The first
// call probes GET /settings and parses the top-level "version"
// field; subsequent calls reuse the cache.
//
// The probe failure mode is captured: if the request errors or the
// response has no parseable version, the cache stores a zero-value
// Version with Known == false, and AtLeast reports false so the
// caller can distinguish "older than this" from "could not tell".
//
// A nil or unconfigured client (baseURL empty) skips the probe
// entirely so test fixtures that build a Client{} to satisfy the
// constructor do not hit a nil httpClient.
func (c *Client) NodeRedVersion(ctx context.Context) Version {
	c.nrVersion.once.Do(func() {
		if c == nil || c.baseURL == "" {
			c.nrVersion.probed.Store(true)
			return
		}
		v := detectNodeRedVersion(ctx, c)
		c.nrVersion.value = v
		c.nrVersion.probed.Store(true)
	})
	if !c.nrVersion.probed.Load() {
		return Version{}
	}
	return c.nrVersion.value
}

// CachedNodeRedVersion returns the cached version without ever
// triggering the probe. A cold cache reports the zero Version
// (Known == false) — the "we could not tell" case the gate
// treats as "do not refuse".
//
// This is the read the runtime version gate (issue #316) needs:
// the gate is invoked synchronously from a request handler, and
// the probe it would otherwise trigger would (a) race the
// handler's own writes and (b) break test fixtures that record
// every request against a strict mock.
//
// ponytail: relies on the banner goroutine in
// internal/mcp/server.go to warm the cache at startup. A server
// whose banner probe was skipped (loopback test fixture) or
// failed (slow / unreachable /settings) therefore never
// enforces the gate. The upgrade path is either an explicit
// "WarmVersionCache" hook the gate awaits, or a shared
// `var onceProbe sync.Once` the gate selects against so
// "unknown" only means "the probe is still running".
func (c *Client) CachedNodeRedVersion() Version {
	if c == nil || !c.nrVersion.probed.Load() {
		return Version{}
	}
	return c.nrVersion.value
}

// detectNodeRedVersion is the probe that backs NodeRedVersion.
// Split out so tests can exercise the parser without the cache
// (the cache is a single atomic load/store, not worth a dedicated
// test).
func detectNodeRedVersion(ctx context.Context, c *Client) Version {
	raw, err := c.GetSettings(ctx)
	if err != nil {
		return Version{}
	}
	return extractVersionField(raw)
}

// extractVersionField pulls "version" out of /settings' opaque
// JSON. Anything more than that risks drift from NR's own schema.
func extractVersionField(raw []byte) Version {
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Version{}
	}
	return ParseVersion(doc.Version)
}

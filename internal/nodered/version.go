package nodered

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
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

// versionCache is a retrying probe wrapper: the first call to
// NodeRedVersion probes the runtime, later calls reuse the cached
// value.
//
// A *successful* probe is cached for the life of the client. A
// *failed* probe is cached only until versionRetryInterval expires:
// the old sync.Once memoized the first failure forever, so a runtime
// that was not up yet when the server booted stayed "unknown" for
// the whole session (issue #340).
//
// mu guards value, probed and lastAttempt. Concurrent callers
// therefore serialize on the probe instead of racing: the first one
// probes, the others find the cache warm (a fresh failure is still
// within the retry interval, so they do not stampede the runtime).
type versionCache struct {
	mu sync.Mutex
	// value is the last probe result. It is read under mu by both
	// accessors, so a reader that sees probed==true also sees the
	// matching value.
	value Version
	// probed distinguishes "the probe ran, here is the result"
	// (known-supported or known-too-low) from "nobody ever asked
	// the runtime yet".
	probed bool
	// lastAttempt is when the last probe ran. It gates the retry
	// after a failed probe.
	lastAttempt time.Time
}

// versionRetryInterval is how long a failed probe is trusted before
// the next caller probes again. A runtime that was unreachable at
// startup is picked up once it comes up, without hammering a
// permanently broken endpoint on every request.
//
// ponytail: a fixed interval is enough; a jittered backoff would
// only matter with many clients probing one dead runtime.
var versionRetryInterval = 30 * time.Second

// NodeRedVersion returns the cached Node-RED version. The first
// call probes GET /settings and parses the top-level "version"
// field; subsequent calls reuse the cache.
//
// The probe failure mode is captured: if the request errors or the
// response has no parseable version, the cache stores a zero-value
// Version with Known == false, and AtLeast reports false so the
// caller can distinguish "older than this" from "could not tell".
//
// That failure is NOT final: once versionRetryInterval has passed,
// the next call probes again, so a runtime that was not reachable at
// startup is detected as soon as it answers (issue #340).
//
// A nil or unconfigured client (baseURL empty) skips the probe
// entirely so test fixtures that build a Client{} to satisfy the
// constructor do not hit a nil httpClient. A client built with
// Options.SkipVersionProbe never probes at all: that explicit
// option is what distinguishes a test fixture from a real local
// install, replacing the old loopback-URL heuristic.
func (c *Client) NodeRedVersion(ctx context.Context) Version {
	if c == nil || c.skipVersionProbe {
		return Version{}
	}
	c.nrVersion.mu.Lock()
	defer c.nrVersion.mu.Unlock()
	if c.nrVersion.probed &&
		(c.nrVersion.value.Known || time.Since(c.nrVersion.lastAttempt) < versionRetryInterval) {
		return c.nrVersion.value
	}
	if c.baseURL == "" {
		c.nrVersion.probed = true
		return Version{}
	}
	v := detectNodeRedVersion(ctx, c)
	c.nrVersion.value = v
	c.nrVersion.probed = true
	c.nrVersion.lastAttempt = time.Now()
	return v
}

// CachedNodeRedVersion returns the cached version without ever
// triggering the probe. A cold cache reports the zero Version
// (Known == false) — the "we could not tell" case the gate
// treats as "do not refuse".
//
// This is the non-probing read: it never surprises the caller
// with a network call. The runtime gate that needs a resolved
// version calls NodeRedVersion instead, which probes on demand
// (issue #340).
func (c *Client) CachedNodeRedVersion() Version {
	if c == nil {
		return Version{}
	}
	c.nrVersion.mu.Lock()
	defer c.nrVersion.mu.Unlock()
	if !c.nrVersion.probed {
		return Version{}
	}
	return c.nrVersion.value
}

// detectNodeRedVersion is the probe that backs NodeRedVersion.
// Split out so tests can exercise the parser without the cache.
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

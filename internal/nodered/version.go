package nodered

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
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
// even with concurrent callers (atomic.Load+Store would race:
// all goroutines see nil and all probe).
type versionCache struct {
	once  sync.Once
	value Version
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
			return
		}
		c.nrVersion.value = detectNodeRedVersion(ctx, c)
	})
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

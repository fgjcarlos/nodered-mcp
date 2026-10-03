package nodered

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestParseVersion_Stable(t *testing.T) {
	v := ParseVersion("5.0.1")
	if v.Major != 5 || v.Minor != 0 || v.Patch != 1 {
		t.Errorf("got %d.%d.%d, want 5.0.1", v.Major, v.Minor, v.Patch)
	}
	if !v.Known {
		t.Errorf("Known should be true for a parseable string")
	}
	if v.Raw != "5.0.1" {
		t.Errorf("Raw should preserve input, got %q", v.Raw)
	}
}

func TestParseVersion_PreRelease(t *testing.T) {
	v := ParseVersion("3.1.0-rc1")
	if v.Major != 3 || v.Minor != 1 || v.Patch != 0 {
		t.Errorf("pre-release tag should not affect triple, got %d.%d.%d", v.Major, v.Minor, v.Patch)
	}
}

func TestParseVersion_BuildMetadata(t *testing.T) {
	v := ParseVersion("2.1.4+build.7")
	if v.Major != 2 || v.Minor != 1 || v.Patch != 4 {
		t.Errorf("build metadata should not affect triple, got %d.%d.%d", v.Major, v.Minor, v.Patch)
	}
}

func TestParseVersion_TwoPart(t *testing.T) {
	v := ParseVersion("5.0")
	if v.Major != 5 || v.Minor != 0 || v.Patch != 0 {
		t.Errorf("two-part version should zero the missing component, got %d.%d.%d", v.Major, v.Minor, v.Patch)
	}
}

func TestParseVersion_Garbage(t *testing.T) {
	cases := []string{"", "   ", "vX.Y.Z", "abc"}
	for _, in := range cases {
		v := ParseVersion(in)
		if v.Known {
			t.Errorf("ParseVersion(%q): Known should be false, got %+v", in, v)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		v    Version
		maj  int
		min  int
		pat  int
		want bool
	}{
		{ParseVersion("5.0.0"), 5, 0, 0, true},
		{ParseVersion("5.0.1"), 5, 0, 0, true},
		{ParseVersion("5.0.0"), 4, 9, 9, true},
		{ParseVersion("4.9.9"), 5, 0, 0, false},
		{ParseVersion("4.9.9"), 4, 10, 0, false},
		{ParseVersion("4.10.0"), 4, 10, 0, true},
		{Version{}, 1, 0, 0, false}, // unknown is always less
	}
	for _, c := range cases {
		if got := c.v.AtLeast(c.maj, c.min, c.pat); got != c.want {
			t.Errorf("%s.AtLeast(%d.%d.%d) = %v, want %v", c.v, c.maj, c.min, c.pat, got, c.want)
		}
	}
}

func TestVersionString(t *testing.T) {
	if got := ParseVersion("3.1.0").String(); got != "3.1.0" {
		t.Errorf("String() should preserve raw, got %q", got)
	}
	if got := (Version{Major: 2, Minor: 1, Patch: 0, Known: true}).String(); got != "2.1.0" {
		t.Errorf("zero-Raw should synthesise, got %q", got)
	}
}

// TestNodeRedVersion_Cached ensures the probe runs at most once
// even with concurrent callers. The mock server counts the /settings
// hits.
func TestNodeRedVersion_Cached(t *testing.T) {
	var hits int
	var mu sync.Mutex
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/settings") {
			mu.Lock()
			hits++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"version":"5.0.1"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = client.NodeRedVersion(context.Background())
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if hits > 1 {
		t.Errorf("probe ran %d times, want <= 1", hits)
	}
	v := client.NodeRedVersion(context.Background())
	if !v.Known || v.Major != 5 || v.Minor != 0 || v.Patch != 1 {
		t.Errorf("got %+v, want 5.0.1", v)
	}
}

// TestNodeRedVersion_ProbeFailure proves the cache captures the
// "could not tell" case (Known == false) so subsequent AtLeast
// checks return false rather than panicking or retrying forever.
func TestNodeRedVersion_ProbeFailure(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	})
	v := client.NodeRedVersion(context.Background())
	if v.Known {
		t.Errorf("expected Known=false on probe failure, got %+v", v)
	}
	v2 := client.NodeRedVersion(context.Background())
	if v2.Known {
		t.Errorf("expected Known=false on cache hit too, got %+v", v2)
	}
}

// TestCachedNodeRedVersion_ColdNoProbe is the contract the
// version gate relies on: a cold cache must report Known==false
// AND must not hit /settings. The gate calls this from inside a
// request handler; a probing accessor would race the handler's
// own writes and trip the test fixtures' strict mock
// handlers (issue #316).
func TestCachedNodeRedVersion_ColdNoProbe(t *testing.T) {
	var hits int
	var mu sync.Mutex
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/settings" {
			mu.Lock()
			hits++
			mu.Unlock()
			t.Errorf("CachedNodeRedVersion must not probe /settings on a cold cache")
		}
		w.WriteHeader(http.StatusOK)
	})

	v := client.CachedNodeRedVersion()
	if v.Known {
		t.Errorf("cold cache must report Known=false, got %+v", v)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Errorf("cold cache should not have hit /settings, got %d hits", hits)
	}
}

// TestCachedNodeRedVersion_MatchesAfterProbe: once NodeRedVersion
// has run, CachedNodeRedVersion returns the same value without
// a re-probe.
func TestCachedNodeRedVersion_MatchesAfterProbe(t *testing.T) {
	var hits int
	var mu sync.Mutex
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/settings" {
			mu.Lock()
			hits++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"version":"5.0.1"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	probed := client.NodeRedVersion(context.Background())
	if !probed.Known || probed.String() != "5.0.1" {
		t.Fatalf("probe did not cache 5.0.1, got %+v", probed)
	}
	mu.Lock()
	hitsAfterProbe := hits
	mu.Unlock()

	cached := client.CachedNodeRedVersion()
	if cached != probed {
		t.Errorf("CachedNodeRedVersion must return the cached value, got %+v want %+v", cached, probed)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != hitsAfterProbe {
		t.Errorf("CachedNodeRedVersion triggered a re-probe (%d -> %d hits)", hitsAfterProbe, hits)
	}
}

// TestExtractVersionField proves we only read the top-level
// "version" key — every other field is opaque to us.
func TestExtractVersionField(t *testing.T) {
	body := []byte(`{"version":"3.1.0","theme":"dark","httpNodeRoot":"/","port":1880}`)
	v := extractVersionField(body)
	if v.String() != "3.1.0" {
		t.Errorf("got %q, want 3.1.0", v)
	}
}

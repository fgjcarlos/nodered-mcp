package main

import "testing"

func TestPickVersion(t *testing.T) {
	tests := []struct {
		name     string
		stamped  string
		embedded string
		want     string
	}{
		{
			// goreleaser passes -X main.version=v1.2.3; that always wins.
			name: "ldflags stamp wins", stamped: "v1.2.3", embedded: "v0.9.0", want: "v1.2.3",
		},
		{
			// `go install module@v0.5.0` applies no ldflags, but the toolchain
			// records the module version. Reporting "dev" there is a lie.
			name:    "falls back to the embedded module version",
			stamped: "dev", embedded: "v0.5.0", want: "v0.5.0",
		},
		{
			// Untagged repo: `go install ...@latest` yields a pseudo-version.
			// Still far more useful than "dev".
			name:    "keeps a pseudo-version",
			stamped: "dev", embedded: "v0.0.0-20260724124424-6cfec97d8ea1", want: "v0.0.0-20260724124424-6cfec97d8ea1",
		},
		{
			// A plain `go build` in a checkout: the toolchain reports "(devel)",
			// which is noise, so we keep the honest "dev".
			name: "ignores (devel)", stamped: "dev", embedded: "(devel)", want: "dev",
		},
		{
			name: "no information at all", stamped: "dev", embedded: "", want: "dev",
		},
		{
			// Defensive: an empty stamp must not produce an empty version.
			name: "empty stamp with no embedded version", stamped: "", embedded: "", want: "dev",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickVersion(tc.stamped, tc.embedded); got != tc.want {
				t.Errorf("pickVersion(%q, %q) = %q, want %q", tc.stamped, tc.embedded, got, tc.want)
			}
		})
	}
}

// resolveVersion reads real build info. In a test binary the toolchain reports
// no usable module version, so this must degrade to the stamped default rather
// than return something empty.
func TestResolveVersionNeverReturnsEmpty(t *testing.T) {
	if got := resolveVersion(); got == "" {
		t.Error("resolveVersion returned an empty string")
	}
}

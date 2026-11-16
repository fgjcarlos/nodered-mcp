package nodered

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// This file is the regression for issue #342: an error must never carry
// the query string or the userinfo of the configured Node-RED URL,
// because either one can hold a token that then reaches the operator's
// logs and the MCP client.

// assertNoToken fails the test if the text carries the synthetic token
// or the userinfo password.
func assertNoToken(t *testing.T, what, got string) {
	t.Helper()
	for _, leak := range []string{"SECRET", "hunter2", "token="} {
		if strings.Contains(got, leak) {
			t.Errorf("%s leaked %q: %q", what, leak, got)
		}
	}
}

// TestGetRaw_APIErrorPathHasNoQueryNorUserinfo covers the 4xx and 5xx
// branches of getRaw: the error path must name the request path, not
// the joined URL, so a token in the base URL cannot leak.
func TestGetRaw_APIErrorPathHasNoQueryNorUserinfo(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("Cannot GET /flow/global"))
			}))
			defer srv.Close()

			// The base URL carries both a userinfo and a query token.
			base := strings.Replace(srv.URL, "http://", "http://user:hunter2@", 1) + "/?token=SECRET"
			c, err := NewClient(Options{BaseURL: base})
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}

			_, err = c.getRaw(context.Background(), "/flow/global")
			if err == nil {
				t.Fatalf("expected an error for status %d", status)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected *APIError, got %T: %v", err, err)
			}
			if apiErr.Path != "/flow/global" {
				t.Errorf("APIError.Path = %q, want the request path %q", apiErr.Path, "/flow/global")
			}
			assertNoToken(t, "APIError.Path", apiErr.Path)
			assertNoToken(t, "error message", err.Error())
		})
	}
}

// TestGetRaw_NetworkFailureHasNoToken covers the transport branch:
// the destination is named redacted, so the query string never shows up.
func TestGetRaw_NetworkFailureHasNoToken(t *testing.T) {
	c, err := NewClient(Options{BaseURL: "http://user:hunter2@127.0.0.1:1/?token=SECRET"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.getRaw(context.Background(), "/flow/global")
	if err == nil {
		t.Fatal("expected an error against a closed port")
	}
	assertNoToken(t, "error message", err.Error())
}

// TestGetRaw_InvalidBaseURLHasNoToken covers the configuration branch:
// the base URL cannot be parsed, and the parse error must not echo it.
func TestGetRaw_InvalidBaseURLHasNoToken(t *testing.T) {
	c, err := NewClient(Options{BaseURL: "http://127.0.0.1:1x/?token=SECRET"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.getRaw(context.Background(), "/flow/global")
	if err == nil {
		t.Fatal("expected an error for an invalid base URL")
	}
	assertNoToken(t, "error message", err.Error())
}

// TestDo_URLBuildErrorHasNoToken covers the same configuration branch
// on the JSON path: an unparsable base URL must not be echoed.
func TestDo_URLBuildErrorHasNoToken(t *testing.T) {
	c, err := NewClient(Options{BaseURL: "http://127.0.0.1:1x/?token=SECRET"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.do(context.Background(), http.MethodGet, "/flows", nil, nil)
	if err == nil {
		t.Fatal("expected an error for an invalid base URL")
	}
	assertNoToken(t, "error message", err.Error())
}

// TestDo_APIErrorPathIsTheRequestPath pins the behaviour the JSON path
// already had: the error names the path segment, not the URL.
func TestDo_APIErrorPathIsTheRequestPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Cannot GET /flows"))
	}))
	defer srv.Close()

	c, err := NewClient(Options{BaseURL: srv.URL + "/?token=SECRET"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.do(context.Background(), http.MethodGet, "/flows", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Path != "/flows" {
		t.Errorf("APIError.Path = %q, want %q", apiErr.Path, "/flows")
	}
	assertNoToken(t, "error message", err.Error())
}

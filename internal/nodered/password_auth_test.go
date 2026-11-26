package nodered

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestAdminPasswordAuth(t *testing.T) {
	for _, tc := range []struct {
		name, mode, wantErr   string
		expiry, grants, calls int
	}{
		{"cache", "ok", "", 3600, 1, 2},
		{"expired", "ok", "", 0, 2, 2},
		{"retry", "reject-first", "", 3600, 2, 3},
		{"retry only once", "reject-all", "HTTP 401", 3600, 2, 2},
		{"wrong credentials", "bad-password", "username or password", 3600, 1, 0},
		{"no login", "anonymous", "", 3600, 0, 2},
		{"static token", "static", "HTTP 401", 3600, 0, 1},
		{"no credentials", "unconfigured", "", 3600, 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const password = "test-password-446"
			const token = "test-token-446"
			var mu sync.Mutex
			var probes, grants, calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/admin/auth/login":
					probes++
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
						t.Error("expected anonymous GET login probe")
					}
					if tc.mode == "anonymous" {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"type":"credentials"}`)
					}
				case "/admin/auth/token":
					grants++
					var form map[string]string
					if err := json.NewDecoder(r.Body).Decode(&form); err != nil || r.Header.Get("Content-Type") != "application/json" {
						t.Error("expected JSON grant")
					}
					if r.Method != http.MethodPost || form["username"] != "operator" || form["password"] != password || form["client_id"] != "node-red-admin" || form["grant_type"] != "password" || form["scope"] != "*" || r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
						t.Error("incorrect password grant request")
					}
					if tc.mode == "bad-password" {
						http.Error(w, password+" "+token, http.StatusForbidden)
						return
					}
					fmt.Fprintf(w, `{"access_token":"%s-%d","expires_in":%d,"token_type":"Bearer"}`, token, grants, tc.expiry)
				case "/admin/flows", "/admin/log":
					calls++
					want := fmt.Sprintf("Bearer %s-%d", token, grants)
					switch tc.mode {
					case "anonymous", "unconfigured":
						want = ""
					case "static":
						want = "Bearer fixed-test-token"
					}
					if r.Header.Get("Authorization") != want {
						t.Error("unexpected Authorization header")
					}
					if tc.mode == "reject-all" || tc.mode == "static" || tc.mode == "reject-first" && calls == 1 {
						http.Error(w, password+" "+want, http.StatusUnauthorized)
						return
					}
					if r.URL.Path == "/admin/flows" {
						if r.Method != http.MethodPost {
							t.Error("expected POST")
						}
						buf, err := io.ReadAll(r.Body)
						if err != nil || string(buf) != `{"x":1}` {
							t.Error("request body was not preserved")
						}
					}
					fmt.Fprint(w, `{}`)
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			opts := Options{BaseURL: srv.URL + "/admin", Username: "operator", Password: password}
			if tc.mode == "static" {
				opts.Token = "fixed-test-token"
			}
			if tc.mode == "unconfigured" {
				opts.Username, opts.Password = "", ""
			}
			c, err := NewClient(opts)
			if err != nil {
				t.Fatal(err)
			}
			err = c.do(context.Background(), http.MethodPost, "/flows", map[string]int{"x": 1}, nil)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = c.getRaw(context.Background(), "/log"); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) || strings.Contains(err.Error(), "fixed-test-token") || tc.mode != "static" && (strings.Contains(err.Error(), password) || strings.Contains(err.Error(), token)) {
				t.Error("incorrect or unredacted error")
			}
			mu.Lock()
			defer mu.Unlock()
			wantProbes := 1
			if tc.mode == "static" || tc.mode == "unconfigured" {
				wantProbes = 0
			}
			if probes != wantProbes || grants != tc.grants || calls != tc.calls {
				t.Errorf("probes/grants/calls = %d/%d/%d, want %d/%d/%d", probes, grants, calls, wantProbes, tc.grants, tc.calls)
			}
		})
	}
}

func TestAdminPasswordAuthTailsShareGrant(t *testing.T) {
	var grants atomic.Int32
	topics := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			fmt.Fprint(w, `{"type":"credentials"}`)
		case "/auth/token":
			grants.Add(1)
			fmt.Fprint(w, `{"access_token":"test-tail-bearer","expires_in":3600}`)
		case "/comms":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error("could not accept tail")
				return
			}
			defer conn.CloseNow()
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			_, data, err := conn.Read(ctx)
			if err != nil || string(data) != `{"auth":"test-tail-bearer"}` {
				t.Error("tail did not authenticate with the exchanged token")
				return
			}
			if err := conn.Write(ctx, websocket.MessageText, []byte(`{"auth":"ok"}`)); err != nil {
				t.Error("could not confirm tail auth")
				return
			}
			_, data, err = conn.Read(ctx)
			var frame map[string]string
			if err != nil || json.Unmarshal(data, &frame) != nil {
				t.Error("tail did not subscribe")
				return
			}
			select {
			case topics <- frame["subscribe"]:
			case <-ctx.Done():
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c, err := NewClient(Options{BaseURL: server.URL, Username: "operator", Password: "test-tail-password"})
	if err != nil {
		t.Fatal(err)
	}
	debug, err := NewDebugTail(c, 1)
	if err != nil {
		t.Fatal(err)
	}
	status, err := NewStatusTail(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	var wg sync.WaitGroup
	for _, run := range []func(context.Context){debug.Run, status.Run} {
		wg.Add(1)
		go func() { defer wg.Done(); run(ctx) }()
	}
	defer func() { cancel(); wg.Wait() }()
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case topic := <-topics:
			seen[topic] = true
		case <-ctx.Done():
			t.Fatal("both tails did not subscribe")
		}
	}
	if !seen["debug"] || !seen["status/#"] || grants.Load() != 1 {
		t.Error("debug and status did not use one shared grant")
	}
}

func TestAdminPasswordGrantDoesNotFollowRedirect(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/login" {
			fmt.Fprint(w, `{"type":"credentials"}`)
			return
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c, err := NewClient(Options{BaseURL: source.URL, Username: "operator", Password: "test-redirect-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListFlows(context.Background())
	if err == nil || err.Error() != "Node-RED /auth/token returned HTTP 307" || redirected.Load() != 0 {
		t.Error("password grant must reject redirects without contacting the destination")
	}
}

func TestAdminPasswordRejectionStopsTails(t *testing.T) {
	var grants atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			fmt.Fprint(w, `{"type":"credentials"}`)
		case "/auth/token":
			grants.Add(1)
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Invalid resource owner credentials"}`)
		default:
			t.Error("unexpected request after rejected password")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c, err := NewClient(Options{BaseURL: srv.URL, Username: "operator", Password: "test-wrong-tail-password"})
	if err != nil {
		t.Fatal(err)
	}
	debug, err := NewDebugTail(c, 1)
	if err != nil {
		t.Fatal(err)
	}
	status, err := NewStatusTail(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []func(context.Context){debug.Run, status.Run} {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		run(ctx)
		expired := ctx.Err() != nil
		cancel()
		if expired {
			t.Error("tail should exit on rejected credentials, not wait to retry")
		}
	}
	if grants.Load() != 2 {
		t.Errorf("expected one grant per tail, got %d", grants.Load())
	}
}

func TestAdminPasswordFollowsSafeProxyRedirects(t *testing.T) {
	const token = "synthetic-proxy-token"
	var grants atomic.Int32
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			fmt.Fprint(w, `{"type":"credentials"}`)
		case "/auth/token":
			grants.Add(1)
			if r.Method != http.MethodPost {
				t.Error("expected a secure password grant")
			}
			fmt.Fprintf(w, `{"access_token":%q,"expires_in":3600}`, token)
		case "/flows":
			http.Redirect(w, r, "/flows/", http.StatusMovedPermanently)
		case "/flows/":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("trusted redirect lost the bearer")
			}
			fmt.Fprint(w, `[]`)
		case "/runtime":
			http.Redirect(w, r, r.URL.Query().Get("destination"), http.StatusTemporaryRedirect)
		default:
			http.NotFound(w, r)
		}
	}))
	defer secure.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/login" || r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
			t.Error("credentials or authenticated traffic reached the HTTP proxy")
		}
		http.Redirect(w, r, secure.URL+r.URL.Path, http.StatusPermanentRedirect)
	}))
	defer proxy.Close()
	c, err := NewClient(Options{BaseURL: proxy.URL, Username: "operator", Password: "synthetic-proxy-password", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListFlows(context.Background()); err != nil || grants.Load() != 1 {
		t.Errorf("proxy login did not complete: %v", err)
	}
	if _, err := c.getRaw(context.Background(), "/runtime?destination="+url.QueryEscape(proxy.URL+"/downgrade")); err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Errorf("authenticated HTTPS request should reject a downgrade: %v", err)
	}
}

package nodered

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSetFlows_AcceptsAnIndentedTabEntry pins issue #343 at the client:
// the "at least one tab" rule decodes the "type" field of every entry,
// so an entry whose JSON carries a space after the colon is still a
// tab. The first version of the rule searched the payload for the
// bytes `"type":"tab"` and refused a document that a human had merely
// indented.
func TestSetFlows_AcceptsAnIndentedTabEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"old","type":"tab","label":"Old"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/flows":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Options{BaseURL: srv.URL, Token: "t", BackupDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	flows := []json.RawMessage{
		json.RawMessage("{\n\t\"id\": \"tabA\",\n\t\"type\": \"tab\",\n\t\"label\": \"Home\",\n\t\"nodes\": []\n}"),
	}
	if err := c.SetFlows(context.Background(), flows); err != nil {
		t.Fatalf("SetFlows: %v", err)
	}
}

// TestSetFlows_DoesNotCountATabMentionedInsideAProperty pins the other
// half of the rule: only an entry whose own "type" is "tab" counts. A
// tab mentioned inside a property — a nested object, or a piece of text
// — is not a tab, so a deployment that carries no tab is refused before
// the runtime is contacted.
func TestSetFlows_DoesNotCountATabMentionedInsideAProperty(t *testing.T) {
	cases := []struct {
		name  string
		entry string
	}{
		{"nested object", `{"id":"n1","type":"inject","meta":{"type":"tab"}}`},
		{"text in a string", `{"id":"n2","type":"inject","info":"\"type\":\"tab\""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("server should not be called for a no-tab flows array: %s %s", r.Method, r.URL.Path)
			})
			err := c.SetFlows(context.Background(), []json.RawMessage{json.RawMessage(tc.entry)})
			if err == nil {
				t.Fatal("expected error when no entry is a tab")
			}
			var guard *FlowGuardError
			if !errors.As(err, &guard) {
				t.Fatalf("expected a FlowGuardError, got %T: %v", err, err)
			}
			if !strings.Contains(err.Error(), `"tab"`) {
				t.Errorf("error must mention the tab requirement, got %q", err.Error())
			}
		})
	}
}

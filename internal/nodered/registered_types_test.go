package nodered

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestAddNode_RejectsUnregisteredTypeUsingCachedPalette(t *testing.T) {
	paletteReads, flowReads, writes := 0, 0, 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
			http.Error(w, "unexpected write", http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/nodes":
			paletteReads++
			_, _ = w.Write([]byte(`[{"types":["inject","debug"]}]`))
		case "/flows":
			flowReads++
			_, _ = w.Write([]byte(`{"rev":"r1","flows":[{"id":"t1","type":"tab"},{"id":"n1","type":"debug","z":"t1","x":1,"y":1,"wires":[]}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	ctx := context.Background()
	for _, typ := range []string{"missing-node", "Debug", "debug ", "subflow:missing"} {
		want := fmt.Sprintf("node type %q is not registered; use search_nodes to find it or install_node to install it", typ)
		if typ == "subflow:missing" {
			want = `node type "subflow:missing" references subflow "missing", but this configuration carries no definition for it`
		}
		addErr := c.AddNode(ctx, "t1", json.RawMessage(`{"id":"n","type":"`+typ+`","z":"t1","x":1,"y":1,"wires":[]}`))
		updateErr := c.UpdateNode(ctx, "t1", "n1", map[string]json.RawMessage{"type": json.RawMessage(fmt.Sprintf("%q", typ))})
		if addErr == nil || addErr.Error() != want {
			t.Errorf("AddNode type %q error = %v; want %q", typ, addErr, want)
		}
		if updateErr == nil || updateErr.Error() != want {
			t.Errorf("UpdateNode type %q error = %v; want %q", typ, updateErr, want)
		}
	}
	if paletteReads != 1 || flowReads != 8 || writes != 0 {
		t.Fatalf("palette reads=%d, flow reads=%d, writes=%d; want 1, 8, 0", paletteReads, flowReads, writes)
	}
}

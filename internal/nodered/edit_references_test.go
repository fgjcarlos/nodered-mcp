package nodered

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestUpdateNodeReferenceIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name, id, patch, wantError string
	}{
		{"missing group", "fn", `{"g":"missing"}`, `node "fn" references unknown group "missing" in g`},
		{"wired removed outputs", "fn", `{"outputs":0}`, `node "fn" has 0 output(s); ports [0 1] still have wires`},
		{"missing group member", "g", `{"nodes":["missing"]}`, `node "g" references unknown member "missing" in nodes`},
		{"missing link target", "out", `{"links":["missing"]}`, `node "out" references unknown link target "missing" in links`},
		{"missing wire target", "fn", `{"wires":[["missing"]]}`, `node "fn" wires to unknown node "missing"`},
		{"missing owner", "fn", `{"z":"missing"}`, `node "fn" (function): references z="missing" which is neither the owning tab nor an existing node in this flow: Node-RED's runtime will crash on deploy with 'Cannot read properties of undefined (reading wires)' if this is written`},
		{"remove output and its wires together", "fn", `{"outputs":"1","wires":[["sink"],[]],"g":"g","name":"after"}`, ""},
		{"link across tabs", "out", `{"links":["in"]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newExternalChangeRuntime(t)
			defer rt.server.Close()
			const fixture = `[
				{"id":"a","type":"tab"},
				{"id":"g","type":"group","z":"a","x":10,"y":10,"nodes":["fn"]},
				{"id":"fn","type":"function","z":"a","g":"g","x":100,"y":80,"outputs":2,"wires":[["sink"],["sink"]],"func":"return msg;"},
				{"id":"sink","type":"debug","z":"a","x":200,"y":80},
				{"id":"out","type":"link out","z":"a","x":100,"y":180,"links":[],"wires":[["stale"]]},
				{"id":"b","type":"tab"},
				{"id":"in","type":"link in","z":"b","x":100,"y":80,"links":[],"wires":[]}
			]`
			if err := json.Unmarshal([]byte(fixture), &rt.after); err != nil {
				t.Fatal(err)
			}
			var patch map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.patch), &patch); err != nil {
				t.Fatal(err)
			}
			c := clientWithBackup(t, rt.server.URL)
			err := c.UpdateNode(context.Background(), "a", tc.id, patch)
			backups, backupErr := c.ListBackups()
			if backupErr != nil {
				t.Fatal(backupErr)
			}
			if tc.wantError != "" {
				if err == nil || err.Error() != tc.wantError {
					t.Errorf("error = %v, want %s", err, tc.wantError)
				}
				if !reflect.DeepEqual(rt.requests, []string{"GET /flows"}) || rt.posted != nil || len(backups) != 0 {
					t.Errorf("rejected patch made requests %v, deployed %t, and made %d backups", rt.requests, rt.posted != nil, len(backups))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rt.requests, []string{"GET /flows", "POST /flows"}) || len(backups) != 1 {
				t.Errorf("valid patch made requests %v and %d backups", rt.requests, len(backups))
			}
			node := rt.postedNode(t, tc.id)
			for key, raw := range patch {
				var want any
				if err := json.Unmarshal(raw, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(node[key], want) {
					t.Errorf("deployed %s = %v, want %v", key, node[key], want)
				}
			}
		})
	}
}

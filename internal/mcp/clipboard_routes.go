package mcp

// HTTP route collisions on import_flow (issue #409). Node-RED keeps both
// http in nodes registered, but closing either one removes every router
// entry with the same path and method, so deleting a copy silently
// unregisters the original until its tab is redeployed.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// routeCollision is one route of the copy that an existing node serves.
type routeCollision struct{ method, url, nodeID, tabID, tabLabel string }

// httpIn is the route one http in element registers.
type httpIn struct{ ID, Type, Method, URL, Z string }

// httpInRoute decodes raw into the route Node-RED registers for it; ok is
// false when it registers none.
func httpInRoute(raw json.RawMessage) (n httpIn, ok bool) {
	var d struct{ D bool }
	if json.Unmarshal(raw, &n) != nil || json.Unmarshal(raw, &d) != nil || n.Type != "http in" || d.D || n.URL == "" {
		return httpIn{}, false
	}
	// 21-httpin.js compares the method exactly against these five; any
	// other value (there is no "all") registers no route.
	switch n.Method {
	case "get", "post", "put", "patch", "delete":
	default:
		return httpIn{}, false
	}
	if n.URL[0] != '/' {
		n.URL = "/" + n.URL
	}
	return n, true
}

// routeCollisions lists, in clipboard then instance order, each enabled
// http in route of the copy that an enabled http in node on an enabled tab
// of the instance already serves. httpNodeRoot prefixes both sides alike.
// shortcut: nodes inside subflow definitions are not followed; extend if
// a subflow-borne route collision is reported.
func routeCollisions(doc *clipboardImport, current []json.RawMessage) []routeCollision {
	var tabDisabled bool
	_ = json.Unmarshal(doc.tab["disabled"], &tabDisabled) // absent: false
	if tabDisabled {
		return nil
	}
	labels := map[string]string{} // enabled tab id -> label
	for _, raw := range current {
		var t struct {
			ID, Type, Label string
			Disabled        bool
		}
		if json.Unmarshal(raw, &t) == nil && t.Type == "tab" && !t.Disabled {
			labels[t.ID] = t.Label
		}
	}
	var out []routeCollision
	seen := map[routeCollision]bool{}
	for _, it := range doc.nodes {
		copied, ok := httpInRoute(it.raw)
		if !ok {
			continue
		}
		for _, raw := range current {
			n, ok := httpInRoute(raw)
			label, onTab := labels[n.Z]
			if !ok || !onTab || n.Method != copied.Method || n.URL != copied.URL {
				continue
			}
			c := routeCollision{method: n.Method, url: n.URL, nodeID: n.ID, tabID: n.Z, tabLabel: label}
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// routeCollisionNotice is the import_flow result sentence for cs, or "".
func routeCollisionNotice(cs []routeCollision) string {
	if len(cs) == 0 {
		return ""
	}
	routes := make([]string, len(cs))
	for i, c := range cs {
		routes[i] = fmt.Sprintf("[%s] %s by node %q on tab %q (%q)", c.method, c.url, c.nodeID, c.tabID, c.tabLabel)
	}
	return " These http in routes of the copy are already served on this instance: " + strings.Join(routes, "; ") +
		". Both copies stay registered; deleting the copy later also unregisters the original route until its tab is redeployed (disable_flow + enable_flow, or any deploy of that tab)."
}

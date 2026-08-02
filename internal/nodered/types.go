// Package nodered is a thin client for the Node-RED admin HTTP API.
//
// We intentionally keep this client small and explicit — every method maps
// to one admin endpoint. The MCP layer on top of this client decides how
// to expose those operations as tools.
package nodered

import "encoding/json"

// RawFlow is a flow document kept as opaque JSON.
//
// Node-RED's node model is deliberately schemaless: every node type carries
// its own properties (an MQTT node has `topic`/`broker`/`qos`, a function
// node has `func`, an inject node has `payload`/`repeat`, ...). If we tried
// to model that with a fixed Go struct, any field we did not name would be
// dropped on unmarshal — and a read/write round-trip would silently corrupt
// every flow. So we pass flow JSON through verbatim and only parse the
// specific fields we need, at the point we need them (see FlowTabCount).
type RawFlow = json.RawMessage

// NodeModuleInfo describes a single installed node module, as returned
// by GET /nodes. Read-only palette metadata — never written back, so a
// typed view is fine here.
//
// `User` distinguishes a user-installed module from one bundled with
// Node-RED (`Local`). Both fields are emitted by the runtime.
type NodeModuleInfo struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Local   bool     `json:"local"`
	User    bool     `json:"user,omitempty"`
	Types   []string `json:"types,omitempty"`
	Loaded  bool     `json:"loaded"`
	Enabled bool     `json:"enabled"`
	Module  string   `json:"module,omitempty"`
}

// InstallInfo is the payload returned by GET /nodes/:module.
//
// `Local` is true for modules shipped with Node-RED, `User` for modules
// installed by the user from npm. `Path` is the on-disk location of the
// module. `Plugins` is the raw array returned by the runtime — its shape
// is not stable across Node-RED versions, so we preserve it as
// RawMessage and let callers parse what they need.
type InstallInfo struct {
	Name    string           `json:"name"`
	Version string           `json:"version,omitempty"`
	Local   bool             `json:"local,omitempty"`
	User    bool             `json:"user,omitempty"`
	Path    string           `json:"path,omitempty"`
	Plugins json.RawMessage  `json:"plugins,omitempty"`
	Nodes   []NodeModuleInfo `json:"nodes"`
}

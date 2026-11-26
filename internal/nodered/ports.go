package nodered

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrWireExists reports that the wire connect_nodes was asked to create is
// already in the tab: there is nothing to deploy and nothing to back up.
var ErrWireExists = errors.New("wire already exists")

// Core node types that have no input side and no output side, as registered
// in @node-red/nodes (inputs:0 and outputs:0). GET /nodes publishes no port
// counts, so a type outside these tables is judged by its own fields only: a
// subflow instance or a third-party node is assumed to carry both sides.
var (
	coreNodesWithoutInput = map[string]bool{
		"inject": true, "comment": true, "link in": true, "catch": true, "status": true,
		"http in": true, "mqtt in": true, "websocket in": true,
	}
	coreNodesWithoutOutput = map[string]bool{
		"debug": true, "comment": true, "link out": true, "http response": true,
		"mqtt out": true, "websocket out": true,
	}
)

// wireEndpointProblem returns why a wire from src can never deliver a message
// to target, or "" when it can. A config node (isConfig) has neither side.
// connect_nodes and validate_flow share it so both say the same thing.
func wireEndpointProblem(src, target map[string]json.RawMessage, srcIsConfig, targetIsConfig bool) string {
	srcType, _ := jsTypeString(src["type"])
	targetType, _ := jsTypeString(target["type"])
	switch {
	case srcIsConfig:
		return fmt.Sprintf("node %q (%s) is a config node and has no outputs; a wire from it would never deliver a message", nodeID(src), srcType)
	case targetIsConfig:
		return fmt.Sprintf("node %q (%s) is a config node and has no inputs; a wire into it would never deliver a message", nodeID(target), targetType)
	case coreNodesWithoutOutput[srcType]:
		if srcType == "link out" {
			return fmt.Sprintf("node %q (link out) has no outputs; it delivers through its \"links\" array to link in nodes, and the runtime drops a wire from it", nodeID(src))
		}
		return fmt.Sprintf("node %q (%s) has no outputs; a wire from it would never deliver a message", nodeID(src), srcType)
	case coreNodesWithoutInput[targetType]:
		return fmt.Sprintf("node %q (%s) has no inputs; a wire into it would never deliver a message", nodeID(target), targetType)
	}
	return ""
}

// wireEndpoints locates both ends of a wire in the tab and refuses one the
// runtime would never carry (issue #448). It returns the source node, which
// is what the wire is written to.
func wireEndpoints(doc *flowDoc, fromID, toID string) (map[string]json.RawMessage, error) {
	fromCollection, fromIndex, found := doc.locate(fromID)
	if !found {
		return nil, fmt.Errorf("no node with id %q in this flow", fromID)
	}
	toCollection, toIndex, found := doc.locate(toID)
	if !found {
		return nil, fmt.Errorf("cannot wire to %q: no node with that id in this flow", toID)
	}
	from, to := (*fromCollection)[fromIndex], (*toCollection)[toIndex]
	if why := wireEndpointProblem(from, to, fromCollection == &doc.Configs, toCollection == &doc.Configs); why != "" {
		return nil, errors.New(why)
	}
	return from, nil
}

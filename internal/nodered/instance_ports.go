package nodered

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The runtime indexes a subflow instance's wires by the outputs of its
// definition, so a port past them can never fire (issue #473).
// validateInstancePorts is the one guard every write path that carries an
// instance shares: add_node, update_node, connect_nodes and
// instantiate_subflow.
//
// flows is the flat configuration the edit was built on, which is where the
// definition is resolved. A configuration that does not carry it leaves the
// output count unknown, and an unknown count is not a refusal — the same
// policy declaredOutputs applies to a node type it cannot judge. A malformed
// wires value is refused either way.
func validateInstancePorts(node json.RawMessage, flows []json.RawMessage) error {
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(node, &decoded); err != nil {
		return nil
	}
	typ := nodeType(decoded)
	id := strings.TrimPrefix(typ, "subflow:")
	if id == "" || id == typ {
		return nil
	}
	wires, present := decoded["wires"]
	if !present || jsonTypeOf(wires) == "null" {
		return nil
	}
	var ports [][]json.RawMessage
	if err := json.Unmarshal(wires, &ports); err != nil {
		// The guard the deployment already applied, moved ahead of it: the
		// wording stays the one the write path has always produced.
		return &FlowGuardError{Message: invalidWiresMessage(nodeID(decoded), wires)}
	}
	if outputs, known := subflowDefinitionOutputs(id, flows); known && len(ports) > outputs {
		return instanceWiresError(id, outputs, len(ports))
	}
	return nil
}

// subflowDefinitionOutputs is the number of outputs of the definition an
// instance names, resolved from the flat configuration. known is false when
// the configuration does not carry that definition.
func subflowDefinitionOutputs(id string, flows []json.RawMessage) (int, bool) {
	for _, entry := range flows {
		var m map[string]json.RawMessage
		if json.Unmarshal(entry, &m) != nil {
			continue
		}
		if stringField(m, "type") == "subflow" && stringField(m, "id") == id {
			return subflowOutputCount(entry), true
		}
	}
	return 0, false
}

// instanceWiresError is the refusal for an instance wired past the outputs of
// its definition. Both the edit paths and InstantiateSubflow build it here so
// their wording cannot drift apart.
func instanceWiresError(subflowID string, outputs, ports int) error {
	return fmt.Errorf("subflow %q has %d output(s), but the instance wires %d port(s): wires must have at most %d port(s)", subflowID, outputs, ports, outputs)
}

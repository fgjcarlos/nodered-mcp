package nodered

import (
	"encoding/json"
	"strings"
	"testing"
)

// sampleFlows mirrors the shape Node-RED actually returns: a flat array mixing
// tabs, subflows, ordinary nodes (which carry "z"), and config nodes (which do
// not). Two tabs, one subflow, one shared broker, one disabled tab.
const sampleFlows = `[
  {"id":"tab1","type":"tab","label":"Home"},
  {"id":"tab2","type":"tab","label":"Weather","disabled":true},
  {"id":"sf1","type":"subflow","name":"Normalize"},
  {"id":"n1","type":"inject","z":"tab1","name":"every 5s","repeat":"5","payload":"hello"},
  {"id":"n2","type":"mqtt in","z":"tab1","topic":"home/temp","broker":"b1","wires":[["n3"]]},
  {"id":"n3","type":"debug","z":"tab1","name":"show temp"},
  {"id":"n4","type":"http request","z":"tab2","url":"https://api.weather.example/now"},
  {"id":"n5","type":"function","z":"sf1","func":"return msg;"},
  {"id":"b1","type":"mqtt-broker","name":"local broker","broker":"localhost"}
]`

func TestSummarizeFlows(t *testing.T) {
	got := SummarizeFlows(RawFlow(sampleFlows))

	if len(got.Tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(got.Tabs))
	}
	if len(got.Subflows) != 1 {
		t.Fatalf("expected 1 subflow, got %d", len(got.Subflows))
	}
	if got.ConfigNodes != 1 {
		t.Errorf("expected 1 config node (the broker), got %d", got.ConfigNodes)
	}
	if got.TotalNodes != 6 {
		t.Errorf("expected 6 nodes total, got %d", got.TotalNodes)
	}

	home := got.Tabs[0]
	if home.ID != "tab1" || home.Label != "Home" {
		t.Errorf("first tab should be Home/tab1, got %s/%s", home.ID, home.Label)
	}
	if home.NodeCount != 3 {
		t.Errorf("Home should own 3 nodes, got %d", home.NodeCount)
	}
	if home.NodeTypes["inject"] != 1 || home.NodeTypes["mqtt in"] != 1 || home.NodeTypes["debug"] != 1 {
		t.Errorf("unexpected type breakdown for Home: %v", home.NodeTypes)
	}
	if !got.Tabs[1].Disabled {
		t.Error("Weather tab is disabled in the fixture and should report so")
	}
}

// The whole point of the summary is that it is small. If it ever carries node
// bodies again it stops solving the problem it exists for.
func TestSummarizeFlowsOmitsNodeBodies(t *testing.T) {
	out, err := json.Marshal(SummarizeFlows(RawFlow(sampleFlows)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leaked := range []string{"home/temp", "return msg;", "api.weather.example", "payload"} {
		if strings.Contains(string(out), leaked) {
			t.Errorf("summary leaked node body content %q: %s", leaked, out)
		}
	}
	if len(out) >= len(sampleFlows) {
		t.Errorf("summary (%d bytes) is not smaller than the input (%d bytes)", len(out), len(sampleFlows))
	}
}

func TestSummarizeFlowsHandlesEnvelope(t *testing.T) {
	env := RawFlow(`{"rev":"abc","flows":` + sampleFlows + `}`)
	if got := SummarizeFlows(env); len(got.Tabs) != 2 {
		t.Errorf("expected the {rev,flows} envelope to be unwrapped, got %d tabs", len(got.Tabs))
	}
}

func TestSummarizeFlowsOnGarbage(t *testing.T) {
	got := SummarizeFlows(RawFlow(`not json`))
	if len(got.Tabs) != 0 || got.TotalNodes != 0 {
		t.Errorf("garbage input should summarize to nothing, got %+v", got)
	}
}

func TestSearchFlowsByQueryMatchesNodeProperties(t *testing.T) {
	// "home/temp" lives in the mqtt node's topic — a field no fixed schema
	// would know about. Searching the raw node JSON is what finds it.
	matches, total := SearchFlows(RawFlow(sampleFlows), "home/temp", "", 20)

	if total != 1 {
		t.Fatalf("expected 1 match for the mqtt topic, got %d", total)
	}
	if matches[0].FlowID != "tab1" || matches[0].FlowLabel != "Home" {
		t.Errorf("match should be located in Home/tab1, got %s/%s", matches[0].FlowID, matches[0].FlowLabel)
	}
	if !strings.Contains(string(matches[0].Node), `"id":"n2"`) {
		t.Errorf("expected node n2, got %s", matches[0].Node)
	}
}

func TestSearchFlowsIsCaseInsensitive(t *testing.T) {
	if _, total := SearchFlows(RawFlow(sampleFlows), "LOCAL BROKER", "", 20); total != 1 {
		t.Errorf("expected a case-insensitive hit on the broker name, got %d", total)
	}
}

func TestSearchFlowsByType(t *testing.T) {
	matches, total := SearchFlows(RawFlow(sampleFlows), "", "debug", 20)

	if total != 1 {
		t.Fatalf("expected 1 debug node, got %d", total)
	}
	if !strings.Contains(string(matches[0].Node), `"id":"n3"`) {
		t.Errorf("expected node n3, got %s", matches[0].Node)
	}
}

func TestSearchFlowsCombinesQueryAndType(t *testing.T) {
	// "b1" appears in the mqtt node (as its broker ref) and in the broker
	// config node itself. The type filter must narrow it to one.
	_, total := SearchFlows(RawFlow(sampleFlows), "b1", "mqtt-broker", 20)
	if total != 1 {
		t.Errorf("expected the type filter to narrow to 1, got %d", total)
	}
}

// Tabs and subflows are containers, not nodes, and must never appear as hits.
func TestSearchFlowsNeverReturnsContainers(t *testing.T) {
	matches, _ := SearchFlows(RawFlow(sampleFlows), "", "", 100)
	for _, m := range matches {
		if strings.Contains(string(m.Node), `"type":"tab"`) || strings.Contains(string(m.Node), `"type":"subflow"`) {
			t.Errorf("search returned a container: %s", m.Node)
		}
	}
}

// Truncation must be visible. A caller that sees 2 results and no total would
// reasonably conclude there are only 2.
func TestSearchFlowsReportsTotalBeyondLimit(t *testing.T) {
	matches, total := SearchFlows(RawFlow(sampleFlows), "", "", 2)

	if len(matches) != 2 {
		t.Errorf("expected the limit to cap results at 2, got %d", len(matches))
	}
	if total != 6 {
		t.Errorf("expected total to report all 6 nodes despite the limit, got %d", total)
	}
}

func TestSearchFlowsLocatesConfigNodes(t *testing.T) {
	matches, total := SearchFlows(RawFlow(sampleFlows), "", "mqtt-broker", 20)

	if total != 1 {
		t.Fatalf("expected the shared broker to be findable, got %d", total)
	}
	// A config node belongs to no tab; saying so beats an empty string.
	if matches[0].FlowID != "" || matches[0].FlowLabel != "" {
		t.Errorf("config node should report no owning flow, got %q/%q", matches[0].FlowID, matches[0].FlowLabel)
	}
}

func TestSearchFlowsSubflowNodesReportTheirSubflow(t *testing.T) {
	matches, total := SearchFlows(RawFlow(sampleFlows), "return msg", "", 20)
	if total != 1 {
		t.Fatalf("expected 1 hit inside the subflow, got %d", total)
	}
	if matches[0].FlowLabel != "Normalize" {
		t.Errorf("subflow nodes should report the subflow name, got %q", matches[0].FlowLabel)
	}
}

func TestSearchFlowsNoMatch(t *testing.T) {
	matches, total := SearchFlows(RawFlow(sampleFlows), "nonexistent-thing", "", 20)
	if total != 0 || len(matches) != 0 {
		t.Errorf("expected no matches, got %d/%d", len(matches), total)
	}
}

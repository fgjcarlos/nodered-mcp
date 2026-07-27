package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerPrompts wires up the prompt templates. Prompts are inert text —
// they suggest a workflow, they cannot act — so read-only mode keeps them.
func (s *Server) registerPrompts() {
	explain := mcp.NewPrompt("explain_flow",
		mcp.WithPromptDescription("Explain what a Node-RED flow does in natural language."),
		mcp.WithArgument("flow_id",
			mcp.RequiredArgument(),
			mcp.ArgumentDescription("ID of the flow tab to explain."),
		),
	)
	s.mcpServer.AddPrompt(explain, s.handleExplainFlowPrompt)
	s.prompts = append(s.prompts, explain)

	generate := mcp.NewPrompt("generate_flow",
		mcp.WithPromptDescription("Generate a Node-RED flow from a high-level description."),
		mcp.WithArgument("description",
			mcp.RequiredArgument(),
			mcp.ArgumentDescription("Plain-English description of what the flow should do."),
		),
	)
	s.mcpServer.AddPrompt(generate, s.handleGenerateFlowPrompt)
	s.prompts = append(s.prompts, generate)
}

func (s *Server) handleExplainFlowPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	flowID := request.Params.Arguments["flow_id"]
	return &mcp.GetPromptResult{
		Description: "Explain a Node-RED flow",
		Messages: []mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleUser,
				mcp.NewTextContent(fmt.Sprintf(
					"Fetch the flow with id %q using the get_flow tool, then explain in natural language "+
						"what this flow does, what its triggers are, and which external systems it talks to. "+
						"Be concise. Highlight anything that looks suspicious (unbounded loops, missing error "+
						"handlers, hardcoded credentials, etc.).",
					flowID,
				)),
			),
		},
	}, nil
}

// handleGenerateFlowPrompt scaffolds a new flow from a description. We don't
// have to fetch any data — the LLM is expected to call list_flows and
// install_node itself if it needs palette context. Keeping the prompt small
// keeps the LLM in charge of the structure.
func (s *Server) handleGenerateFlowPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	desc := request.Params.Arguments["description"]
	return &mcp.GetPromptResult{
		Description: "Generate a Node-RED flow from a description",
		Messages: []mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleUser,
				mcp.NewTextContent(fmt.Sprintf(
					"Generate a Node-RED flow that does the following:\n\n%s\n\n"+
						"Use list_flows to see what's already there, and search_nodes / install_node "+
						"if you need a module that isn't already in the palette. "+
						"Build the flow as a single JSON object with a `label` and a `nodes` array, "+
						"then create it with create_flow (which will back up the current config first). "+
						"Keep nodes to a minimum, wire them deliberately, and use stable, descriptive IDs.",
					desc,
				)),
			),
		},
	}, nil
}

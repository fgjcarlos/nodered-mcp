package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// resources is the registry of MCP resources exposed by the server.
var resources []mcp.Resource

// registerResources wires up the resources declared in PLAN.md.
//
// Hello-world scope: the resource handlers are registered as placeholders
// returning the data via the tool counterpart. We keep them here so the
// server is shaped like the full v0.1 spec from day one.
func (s *Server) registerResources() {
	flowsRes := mcp.NewResource(
		"nodered://flows/current",
		"Current flows",
		mcp.WithResourceDescription("The full current set of flows (tabs + nodes) in the connected Node-RED instance."),
		mcp.WithMIMEType("application/json"),
	)
	s.mcpServer.AddResource(flowsRes, s.handleFlowsResource)
	resources = append(resources, flowsRes)

	settingsRes := mcp.NewResource(
		"nodered://settings",
		"Server settings",
		mcp.WithResourceDescription("The Node-RED server settings (port, adminAuth, theme, plugins). Read-only."),
		mcp.WithMIMEType("application/json"),
	)
	s.mcpServer.AddResource(settingsRes, s.handleSettingsResource)
	resources = append(resources, settingsRes)

	stateRes := mcp.NewResource(
		"nodered://flows/state",
		"Runtime state",
		mcp.WithResourceDescription("The current runtime state of Node-RED (started/stopped). Read-only."),
		mcp.WithMIMEType("application/json"),
	)
	s.mcpServer.AddResource(stateRes, s.handleFlowsStateResource)
	resources = append(resources, stateRes)
}

func (s *Server) handleFlowsResource(ctx context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	raw, err := s.nrClient.ListFlows(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing flows: %w", err)
	}
	if len(raw) == 0 {
		raw = []byte("[]")
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "nodered://flows/current",
			MIMEType: "application/json",
			Text:     prettyJSON(raw),
		},
	}, nil
}

func (s *Server) handleSettingsResource(ctx context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	raw, err := s.nrClient.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting settings: %w", err)
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "nodered://settings",
			MIMEType: "application/json",
			Text:     prettyJSON(raw),
		},
	}, nil
}

func (s *Server) handleFlowsStateResource(ctx context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	raw, err := s.nrClient.GetFlowsState(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting flows state: %w", err)
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "nodered://flows/state",
			MIMEType: "application/json",
			Text:     prettyJSON(raw),
		},
	}, nil
}

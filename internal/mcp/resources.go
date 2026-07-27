package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerResources wires up the read-only resource views of the connected
// instance. Every resource here is side-effect free, so read-only mode keeps
// all of them.
func (s *Server) registerResources() {
	flowsRes := mcp.NewResource(
		"nodered://flows/current",
		"Current flows",
		mcp.WithResourceDescription("The full current set of flows (tabs + nodes) in the connected Node-RED instance."),
		mcp.WithMIMEType("application/json"),
	)
	s.mcpServer.AddResource(flowsRes, s.handleFlowsResource)
	s.resources = append(s.resources, flowsRes)

	settingsRes := mcp.NewResource(
		"nodered://settings",
		"Server settings",
		mcp.WithResourceDescription("The Node-RED server settings (port, adminAuth, theme, plugins). Read-only."),
		mcp.WithMIMEType("application/json"),
	)
	s.mcpServer.AddResource(settingsRes, s.handleSettingsResource)
	s.resources = append(s.resources, settingsRes)

	stateRes := mcp.NewResource(
		"nodered://flows/state",
		"Runtime state",
		mcp.WithResourceDescription("The current runtime state of Node-RED (started/stopped). Read-only."),
		mcp.WithMIMEType("application/json"),
	)
	s.mcpServer.AddResource(stateRes, s.handleFlowsStateResource)
	s.resources = append(s.resources, stateRes)
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

package nodered

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type registeredTypeCache struct {
	mu    sync.Mutex
	types map[string]bool
}

func (c *Client) registeredTypes(ctx context.Context) (map[string]bool, error) {
	cache := &c.registeredTypesCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.types != nil {
		return cache.types, nil
	}
	modules, err := c.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking registered node types: %w", err)
	}
	types := make(map[string]bool)
	for _, module := range modules {
		for _, typ := range module.Types {
			types[typ] = true
		}
	}
	cache.types = types
	return types, nil
}

func (c *Client) invalidateRegisteredTypes() {
	c.registeredTypesCache.mu.Lock()
	c.registeredTypesCache.types = nil
	c.registeredTypesCache.mu.Unlock()
}

// validateRegisteredNodeTypes checks only the supplied candidate JSON. existing
// carries the current flow array when subflow IDs must be resolved.
func (c *Client) validateRegisteredNodeTypes(ctx context.Context, candidate json.RawMessage, existing []json.RawMessage) error {
	var types map[string]bool
	var subflows map[string][]any
	var value any
	if err := json.Unmarshal(candidate, &value); err != nil {
		return fmt.Errorf("checking candidate node types: %w", err)
	}
	var check func(any) error
	check = func(v any) error {
		switch v := v.(type) {
		case []any:
			for _, child := range v {
				if err := check(child); err != nil {
					return err
				}
			}
		case map[string]any:
			if typ, ok := v["type"].(string); ok {
				if strings.HasPrefix(typ, "subflow:") {
					if subflows == nil {
						values := []any{value}
						for _, raw := range existing {
							var entry any
							if json.Unmarshal(raw, &entry) == nil {
								values = append(values, entry)
							}
						}
						subflows = indexSubflowDefinitions(values)
					}
					id := strings.TrimPrefix(typ, "subflow:")
					if _, ok := subflows[id]; !ok {
						return fmt.Errorf("node type %q references subflow %q, but this configuration carries no definition for it", typ, id)
					}
				} else if typ != "tab" && typ != "group" && typ != "junction" && typ != "subflow" {
					if types == nil {
						var err error
						types, err = c.registeredTypes(ctx)
						if err != nil {
							return err
						}
					}
					if !types[typ] {
						return unregisteredTypeError(typ)
					}
				}
			}
			for _, key := range []string{"nodes", "configs", "subflows"} {
				if err := check(v[key]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return check(value)
}

func unregisteredTypeError(typ string) error {
	return fmt.Errorf("node type %q is not registered; use search_nodes to find it or install_node to install it", typ)
}

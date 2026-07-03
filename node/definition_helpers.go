package node

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"
)

func applyNodeMetadata(base *Base, spec dsl.GraphNodeSpec) {
	if base == nil {
		return
	}
	base.Spec.ID = spec.ID
	if strings.TrimSpace(spec.Name) != "" {
		base.Spec.Name = spec.Name
	}
	if strings.TrimSpace(spec.Description) != "" {
		base.Spec.Description = spec.Description
	}
}

func nodeStateScope(configMap map[string]any) string {
	if _, ok := configMap["state_scope"]; ok {
		return config.String(configMap, "state_scope")
	}
	return DefaultScope
}

func scopedConversationPath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if field == "" {
		if scope == "" {
			return state.Shared(accessors.KeyConversation).String()
		}
		return state.Scope(scope, accessors.KeyConversation).String()
	}
	if scope == "" {
		return state.Shared(accessors.KeyConversation, field).String()
	}
	return state.Scope(scope, accessors.KeyConversation, field).String()
}

func scopedStatePath(scope string, field string) string {
	scope = strings.TrimSpace(scope)
	field = strings.TrimSpace(field)
	if scope == "" {
		return state.Shared(field).String()
	}
	if field == "" {
		return state.Scope(scope).String()
	}
	return state.Scope(scope, field).String()
}

func canonicalContractPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "*" {
		return path
	}
	if parsed, err := state.ParsePath(path); err == nil {
		return parsed.String()
	}
	return path
}

func parseOptionalStatePath(text string) (state.Path, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return state.Path{}, nil
	}
	return parseRequiredStatePath(text)
}

func parseRequiredStatePath(text string) (state.Path, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return state.Path{}, fmt.Errorf("state path is required")
	}
	return state.ParsePath(text)
}

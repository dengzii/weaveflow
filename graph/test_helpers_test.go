package graph

import (
	"context"
	"weaveflow/builder"
	"weaveflow/dsl"
	"weaveflow/nodes"
	wfregistry "weaveflow/registry"
	wfstate "weaveflow/state"
)

type collectIteratorItemNode struct {
	id             string
	iteratorNodeID string
	targetKey      string
}

func (n collectIteratorItemNode) ID() string          { return n.id }
func (n collectIteratorItemNode) Name() string        { return n.id }
func (n collectIteratorItemNode) Description() string { return "collect iterator item" }
func (n collectIteratorItemNode) Execute(ctx context.Context, state wfstate.State) (wfstate.StatePatch, error) {
	_ = ctx

	if state == nil {
		return wfstate.StatePatch{}, nil
	}

	namespace := state.Namespace(nodes.IteratorStateNamespace)
	if namespace == nil {
		return wfstate.StatePatch{}, nil
	}
	rawIteratorState, ok := namespace[n.iteratorNodeID]
	if !ok {
		return wfstate.StatePatch{}, nil
	}

	iteratorState, ok := rawIteratorState.(map[string]any)
	if !ok {
		if typed, ok := rawIteratorState.(wfstate.State); ok {
			iteratorState = typed
		} else {
			return wfstate.StatePatch{}, nil
		}
	}

	item, ok := iteratorState["item"].(string)
	if !ok || item == "" {
		return wfstate.StatePatch{}, nil
	}

	return wfstate.StatePatch{
		n.targetKey: []string{item},
	}, nil
}

func registerCollectIteratorItemNodeType(registry *wfregistry.Registry) {
	registry.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "collect_iterator_item",
			Description: "Collect the current iterator item into a string slice.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"iterator_node_id": dsl.JSONSchema{"type": "string"},
					"target_key":       dsl.JSONSchema{"type": "string"},
				},
				"required":             []string{"iterator_node_id", "target_key"},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			iteratorNodeID := wfregistry.StringConfig(spec.Config, "iterator_node_id")
			targetKey := wfregistry.StringConfig(spec.Config, "target_key")
			fields := make([]dsl.StateFieldRef, 0, 2)
			if iteratorNodeID != "" {
				fields = append(fields, dsl.StateFieldRef{
					Path: nodes.IteratorStateRootKey + "." + iteratorNodeID + ".item",
					Mode: dsl.StateAccessRead,
				})
			}
			if targetKey != "" {
				fields = append(fields, dsl.StateFieldRef{
					Path:          targetKey,
					Mode:          dsl.StateAccessReadWrite,
					MergeStrategy: dsl.StateMergeAppend,
				})
			}
			return dsl.StateContract{Fields: fields}, nil
		},
		Build: builder.AdaptNodeBuilder(func(ctx *builder.BuildContext, spec dsl.GraphNodeSpec) (nodes.Node, error) {
			_ = ctx
			return collectIteratorItemNode{
				id:             spec.ID,
				iteratorNodeID: wfregistry.StringConfig(spec.Config, "iterator_node_id"),
				targetKey:      wfregistry.StringConfig(spec.Config, "target_key"),
			}, nil
		}),
	})
}

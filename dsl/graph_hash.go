package dsl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

const graphHashPrefix = "sha256:"

type StateBindingSemantic struct {
	ComponentType string                       `json:"component_type"`
	ComponentID   string                       `json:"component_id,omitempty"`
	EdgeIndex     int                          `json:"edge_index,omitempty"`
	Port          string                       `json:"port"`
	Path          string                       `json:"path"`
	Capability    string                       `json:"capability,omitempty"`
	Contract      []StateContractSemanticField `json:"contract,omitempty"`
}

type StateContractSemanticField struct {
	Path          string             `json:"path"`
	Mode          StateAccessMode    `json:"mode"`
	Required      bool               `json:"required,omitempty"`
	MergeStrategy StateMergeStrategy `json:"merge_strategy"`
	Type          string             `json:"type,omitempty"`
}

// SemanticGraphHash returns a stable hash for the executable graph definition.
//
// Metadata is excluded because it is preserved by graph definitions for UI and
// tooling state, but it is not consumed when building or running a graph.
func SemanticGraphHash(def GraphDefinition) (string, error) {
	return SemanticGraphHashWithStateBindings(def, nil)
}

func SemanticGraphHashWithStateBindings(def GraphDefinition, bindings []StateBindingSemantic) (string, error) {
	normalized := NormalizeGraphDefinition(def)
	normalized.Metadata = nil
	normalized.Nodes = canonicalGraphNodes(normalized.Nodes)
	normalized.StateModules = canonicalStateModules(normalized.StateModules)
	if len(bindings) > 0 {
		return hashCanonicalJSON(struct {
			Definition    GraphDefinition        `json:"definition"`
			StateBindings []StateBindingSemantic `json:"resolved_state_bindings"`
		}{
			Definition:    normalized,
			StateBindings: canonicalStateBindingSemantics(bindings),
		})
	}
	return hashCanonicalJSON(normalized)
}

func canonicalStateBindingSemantics(bindings []StateBindingSemantic) []StateBindingSemantic {
	out := make([]StateBindingSemantic, len(bindings))
	for index, binding := range bindings {
		out[index] = binding
		if len(binding.Contract) > 0 {
			out[index].Contract = append([]StateContractSemanticField(nil), binding.Contract...)
			sort.SliceStable(out[index].Contract, func(left, right int) bool {
				if out[index].Contract[left].Path != out[index].Contract[right].Path {
					return out[index].Contract[left].Path < out[index].Contract[right].Path
				}
				return out[index].Contract[left].Mode < out[index].Contract[right].Mode
			})
		}
	}
	sort.SliceStable(out, func(left, right int) bool {
		if out[left].ComponentType != out[right].ComponentType {
			return out[left].ComponentType < out[right].ComponentType
		}
		if out[left].ComponentID != out[right].ComponentID {
			return out[left].ComponentID < out[right].ComponentID
		}
		if out[left].EdgeIndex != out[right].EdgeIndex {
			return out[left].EdgeIndex < out[right].EdgeIndex
		}
		return out[left].Port < out[right].Port
	})
	return out
}

func canonicalStateModules(modules []StateModuleRef) []StateModuleRef {
	if len(modules) == 0 {
		return nil
	}
	out := append([]StateModuleRef(nil), modules...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Version < out[j].Version
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// SnapshotGraphHash returns a stable hash for the full normalized graph
// definition, including metadata.
func SnapshotGraphHash(def GraphDefinition) (string, error) {
	return hashCanonicalJSON(NormalizeGraphDefinition(def))
}

func hashCanonicalJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return graphHashPrefix + hex.EncodeToString(sum[:]), nil
}

func canonicalGraphNodes(nodes []GraphNodeSpec) []GraphNodeSpec {
	if len(nodes) == 0 {
		return nil
	}
	out := append([]GraphNodeSpec(nil), nodes...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

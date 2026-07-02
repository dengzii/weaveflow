package dsl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

const graphHashPrefix = "sha256:"

// SemanticGraphHash returns a stable hash for the executable graph definition.
//
// Metadata is excluded because it is preserved by graph definitions for UI and
// tooling state, but it is not consumed when building or running a graph.
func SemanticGraphHash(def GraphDefinition) (string, error) {
	normalized := NormalizeGraphDefinition(def)
	normalized.Metadata = nil
	normalized.Nodes = canonicalGraphNodes(normalized.Nodes)
	return hashCanonicalJSON(normalized)
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

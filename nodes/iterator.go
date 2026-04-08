package nodes

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
)

const (
	IteratorStateNamespace = "iterator"
	IteratorStateRootKey   = fruntime.StateNamespacePrefix + IteratorStateNamespace
)

type IteratorNode struct {
	NodeInfo
	StateKey      string
	MaxIterations int
	ContinueTo    string
	DoneTo        string
}

func NewIteratorNode() *IteratorNode {
	id := uuid.New()
	return &IteratorNode{
		NodeInfo: NodeInfo{
			NodeID:          "Iterator_" + id.String(),
			NodeName:        "Iterator",
			NodeDescription: "Iterate over a state array and expose the current iteration in temporary state.",
		},
		MaxIterations: 1,
	}
}

func (n *IteratorNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	_ = ctx

	if state == nil {
		state = fruntime.State{}
	}
	if n.StateKey == "" {
		return state, fmt.Errorf("iterator node %q state_key is required", n.ID())
	}
	if n.MaxIterations <= 0 {
		return state, fmt.Errorf("iterator node %q max_iterations must be greater than 0", n.ID())
	}

	source, ok := fruntime.ResolveStatePath(state, n.StateKey)
	if !ok {
		source = nil
	}
	items, err := iteratorValues(source)
	if err != nil {
		return state, fmt.Errorf("iterator node %q state key %q: %w", n.ID(), n.StateKey, err)
	}

	total := len(items)
	limit := total
	if limit > n.MaxIterations {
		limit = n.MaxIterations
	}

	runtimeState := iteratorNodeRuntimeState(state, n.ID())
	nextIndex, _ := iteratorInt(runtimeState["next_index"])
	if nextIndex < 0 {
		nextIndex = 0
	}

	if nextIndex >= limit {
		writeIteratorDoneState(runtimeState, n.StateKey, total, limit, nextIndex)
		return state, nil
	}

	runtimeState["state_key"] = n.StateKey
	runtimeState["index"] = nextIndex
	runtimeState["iteration"] = nextIndex + 1
	runtimeState["item"] = cloneIteratorValue(items[nextIndex])
	runtimeState["total"] = total
	runtimeState["limit"] = limit
	runtimeState["next_index"] = nextIndex + 1
	runtimeState["done"] = false
	runtimeState["is_first"] = nextIndex == 0
	runtimeState["is_last"] = nextIndex == limit-1
	return state, nil
}

func (n *IteratorNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"state_key":      n.StateKey,
		"max_iterations": n.MaxIterations,
	}
	if n.ContinueTo != "" {
		config["continue_to"] = n.ContinueTo
	}
	if n.DoneTo != "" {
		config["done_to"] = n.DoneTo
	}

	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "iterator",
		Description: n.Description(),
		Config:      config,
	}
}

func iteratorNodeRuntimeState(state fruntime.State, nodeID string) fruntime.State {
	if state == nil {
		return nil
	}

	namespace := state.EnsureNamespace(IteratorStateNamespace)
	if namespace == nil {
		return nil
	}

	switch typed := namespace[nodeID].(type) {
	case fruntime.State:
		return typed
	case map[string]any:
		nested := fruntime.State(typed)
		namespace[nodeID] = nested
		return nested
	default:
		nested := fruntime.State{}
		namespace[nodeID] = nested
		return nested
	}
}

func writeIteratorDoneState(target fruntime.State, stateKey string, total int, limit int, nextIndex int) {
	if target == nil {
		return
	}
	target["state_key"] = stateKey
	target["total"] = total
	target["limit"] = limit
	target["next_index"] = nextIndex
	target["done"] = true
	delete(target, "item")
	delete(target, "index")
	delete(target, "iteration")
	delete(target, "is_first")
	delete(target, "is_last")
}

func iteratorValues(raw any) ([]any, error) {
	switch typed := raw.(type) {
	case nil:
		return nil, nil
	case []any:
		return append([]any(nil), typed...), nil
	case []string:
		values := make([]any, 0, len(typed))
		for _, item := range typed {
			values = append(values, item)
		}
		return values, nil
	case []map[string]any:
		values := make([]any, 0, len(typed))
		for _, item := range typed {
			values = append(values, item)
		}
		return values, nil
	case []fruntime.State:
		values := make([]any, 0, len(typed))
		for _, item := range typed {
			values = append(values, item)
		}
		return values, nil
	}

	value := reflect.ValueOf(raw)
	if !value.IsValid() {
		return nil, nil
	}
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return nil, fmt.Errorf("must be an array or slice, got %T", raw)
	}

	values := make([]any, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		values = append(values, value.Index(i).Interface())
	}
	return values, nil
}

func iteratorInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func cloneIteratorValue(value any) any {
	switch typed := value.(type) {
	case fruntime.State:
		return cloneIteratorStateMap(typed)
	case map[string]any:
		return map[string]any(cloneIteratorStateMap(typed))
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneIteratorValue(item)
		}
		return cloned
	case []string:
		cloned := make([]string, len(typed))
		copy(cloned, typed)
		return cloned
	case []map[string]any:
		cloned := make([]map[string]any, len(typed))
		for i, item := range typed {
			cloned[i] = map[string]any(cloneIteratorStateMap(item))
		}
		return cloned
	default:
		return typed
	}
}

func cloneIteratorStateMap(values map[string]any) fruntime.State {
	if values == nil {
		return nil
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	cloned := make(fruntime.State, len(values))
	for _, key := range keys {
		cloned[key] = cloneIteratorValue(values[key])
	}
	return cloned
}

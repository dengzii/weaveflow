package runtime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type JSONStateCodec struct {
	version string
}

func NewJSONStateCodec(version string) *JSONStateCodec {
	version = strings.TrimSpace(version)
	if version == "" {
		version = DefaultStateVersion
	}
	return &JSONStateCodec{version: version}
}

func (c *JSONStateCodec) Name() string {
	return "json"
}

func (c *JSONStateCodec) Version() string {
	return c.version
}

func (c *JSONStateCodec) Encode(snapshot StateSnapshot) ([]byte, error) {
	if snapshot.Version == "" {
		snapshot.Version = c.version
	}
	return json.Marshal(snapshot)
}

func (c *JSONStateCodec) Decode(data []byte) (StateSnapshot, error) {
	var snapshot StateSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return StateSnapshot{}, err
	}
	if looksLikeLegacyScopeLayout(snapshot.Version, snapshot.Scopes) {
		return StateSnapshot{}, fmt.Errorf("legacy state snapshot layout is no longer supported")
	}
	if snapshot.Version == "" {
		snapshot.Version = c.version
	}
	return snapshot, nil
}

func (c *JSONStateCodec) Diff(before, after StateSnapshot) ([]StateChange, error) {
	beforeFlat, err := flattenSnapshot(before)
	if err != nil {
		return nil, err
	}
	afterFlat, err := flattenSnapshot(after)
	if err != nil {
		return nil, err
	}

	paths := make(map[string]struct{}, len(beforeFlat)+len(afterFlat))
	for key := range beforeFlat {
		paths[key] = struct{}{}
	}
	for key := range afterFlat {
		paths[key] = struct{}{}
	}

	keys := make([]string, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	changes := make([]StateChange, 0, len(keys))
	for _, key := range keys {
		left := beforeFlat[key]
		right := afterFlat[key]
		if jsonEqual(left, right) {
			continue
		}
		changes = append(changes, StateChange{Path: key, Before: left, After: right})
	}
	return changes, nil
}

func SnapshotFromState(state State) (StateSnapshot, error) {
	snapshot := StateSnapshot{
		Version:  NewJSONStateCodec("").Version(),
		Shared:   GraphState{},
		Scopes:   map[string]GraphState{},
		Internal: map[string]GraphState{},
	}

	for _, extension := range defaultStateExtensions() {
		if err := extension.ExtractRootSnapshot(state, &snapshot); err != nil {
			return StateSnapshot{}, err
		}
	}

	for scopeName, scopeState := range state.scopes() {
		scope, err := encodeGraphState(scopeState)
		if err != nil {
			return StateSnapshot{}, err
		}
		snapshot.Scopes[scopeName] = scope
	}

	for key, value := range state {
		if isSpecialStateKey(key) || isInfrastructureStateKey(key) {
			continue
		}
		if isInternalSnapshotNamespaceKey(key) {
			namespace, ok := asStateMap(value)
			if !ok {
				return StateSnapshot{}, fmt.Errorf("internal state namespace %q must be a map[string]any, got %T", key, value)
			}
			encoded, err := encodeGraphState(namespace)
			if err != nil {
				return StateSnapshot{}, fmt.Errorf("marshal internal namespace %q: %w", key, err)
			}
			if encoded != nil {
				snapshot.Internal[key] = encoded
			}
			continue
		}

		if err := validatePersistableStateValue(key, value); err != nil {
			return StateSnapshot{}, err
		}
		raw, err := encodeGraphValue(key, value)
		if err != nil {
			return StateSnapshot{}, fmt.Errorf("marshal state key %q: %w", key, err)
		}
		snapshot.Shared[key] = raw
	}

	if len(snapshot.Shared) == 0 {
		snapshot.Shared = nil
	}
	if len(snapshot.Scopes) == 0 {
		snapshot.Scopes = nil
	}
	if len(snapshot.Internal) == 0 {
		snapshot.Internal = nil
	}

	return snapshot, nil
}

func SnapshotFromStateWithRuntime(state State, runtime RuntimeState, artifacts []ArtifactRef) (StateSnapshot, error) {
	snapshot, err := SnapshotFromState(state)
	if err != nil {
		return StateSnapshot{}, err
	}
	snapshot.Runtime = runtime
	if len(artifacts) > 0 {
		snapshot.Artifacts = cloneArtifactRefs(artifacts)
	}
	return snapshot, nil
}

func RestoreStateSnapshot(snapshot StateSnapshot) (RestoredStateSnapshot, error) {
	state := State{}
	for _, extension := range defaultStateExtensions() {
		if err := extension.ApplyRootSnapshot(state, snapshot); err != nil {
			return RestoredStateSnapshot{}, err
		}
	}

	for key, raw := range snapshot.Shared {
		value, err := decodeGraphValue(key, raw)
		if err != nil {
			return RestoredStateSnapshot{}, fmt.Errorf("unmarshal state key %q: %w", key, err)
		}
		applyDecodedGraphValue(state, key, value)
	}

	for key, scope := range snapshot.Scopes {
		scopeState := State{}
		for valueKey, raw := range scope {
			value, err := decodeGraphValue(valueKey, raw)
			if err != nil {
				return RestoredStateSnapshot{}, fmt.Errorf("unmarshal scope %q key %q: %w", key, valueKey, err)
			}
			applyDecodedGraphValue(scopeState, valueKey, value)
		}
		setScopeState(state, key, scopeState)
	}

	for key, scope := range snapshot.Internal {
		namespaceState := State{}
		for valueKey, raw := range scope {
			value, err := decodeGraphValue(valueKey, raw)
			if err != nil {
				return RestoredStateSnapshot{}, fmt.Errorf("unmarshal internal namespace %q key %q: %w", key, valueKey, err)
			}
			applyDecodedGraphValue(namespaceState, valueKey, value)
		}
		state[key] = namespaceState
	}

	return RestoredStateSnapshot{
		Snapshot:  snapshot,
		Business:  state,
		Runtime:   snapshot.Runtime,
		Artifacts: cloneArtifactRefs(snapshot.Artifacts),
	}, nil
}

func StateFromSnapshot(snapshot StateSnapshot) (State, error) {
	restored, err := RestoreStateSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	return restored.Business, nil
}

func SerializeMessages(messages []llms.MessageContent) ([]StateMessage, error) {
	return serializeMessages(messages)
}

func encodeGraphState(values map[string]any) (GraphState, error) {
	scope := GraphState{}
	for _, extension := range defaultStateExtensions() {
		if err := extension.EncodeScopedState(values, scope); err != nil {
			return nil, err
		}
	}
	for key, value := range values {
		if isInfrastructureStateKey(key) || isSpecialStateKey(key) {
			continue
		}
		if err := validatePersistableStateValue(key, value); err != nil {
			return nil, err
		}
		raw, err := encodeGraphValue(key, value)
		if err != nil {
			return nil, fmt.Errorf("marshal scope key %q: %w", key, err)
		}
		scope[key] = raw
	}
	if len(scope) == 0 {
		return nil, nil
	}
	return scope, nil
}

func applyDecodedGraphValue(target State, key string, value any) {
	if target == nil {
		return
	}
	for _, extension := range defaultStateExtensions() {
		if extension.DecodeStateField(target, key, value) {
			return
		}
	}
	target[key] = value
}

func serializeMessages(messages []llms.MessageContent) ([]StateMessage, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	result := make([]StateMessage, 0, len(messages))
	for _, message := range messages {
		item := StateMessage{Role: string(message.Role)}
		for _, part := range message.Parts {
			encoded, err := serializeMessagePart(part)
			if err != nil {
				return nil, err
			}
			item.Parts = append(item.Parts, encoded)
		}
		result = append(result, item)
	}
	return result, nil
}

func deserializeMessages(messages []StateMessage) ([]llms.MessageContent, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	result := make([]llms.MessageContent, 0, len(messages))
	for _, message := range messages {
		item := llms.MessageContent{
			Role:  llms.ChatMessageType(message.Role),
			Parts: []llms.ContentPart{},
		}
		for _, part := range message.Parts {
			decoded, err := deserializeMessagePart(part)
			if err != nil {
				return nil, err
			}
			item.Parts = append(item.Parts, decoded)
		}
		result = append(result, item)
	}
	return result, nil
}

func serializeMessagePart(part llms.ContentPart) (StateMessagePart, error) {
	switch typed := part.(type) {
	case llms.TextContent:
		return StateMessagePart{Kind: "text", Text: typed.Text}, nil
	case llms.ImageURLContent:
		return StateMessagePart{Kind: "image_url", URL: typed.URL, Detail: typed.Detail}, nil
	case llms.BinaryContent:
		return StateMessagePart{
			Kind:     "binary",
			MIMEType: typed.MIMEType,
			Data:     base64.StdEncoding.EncodeToString(typed.Data),
		}, nil
	case llms.ToolCall:
		part := StateMessagePart{
			Kind:       "tool_call",
			ToolCallID: typed.ID,
			ToolType:   typed.Type,
		}
		if typed.FunctionCall != nil {
			part.FunctionName = typed.FunctionCall.Name
			part.Arguments = typed.FunctionCall.Arguments
		}
		return part, nil
	case llms.ToolCallResponse:
		return StateMessagePart{
			Kind:       "tool_response",
			ToolCallID: typed.ToolCallID,
			Name:       typed.Name,
			Content:    typed.Content,
		}, nil
	default:
		return StateMessagePart{}, fmt.Errorf("unsupported message part type %T", part)
	}
}

func deserializeMessagePart(part StateMessagePart) (llms.ContentPart, error) {
	switch part.Kind {
	case "text":
		return llms.TextPart(part.Text), nil
	case "image_url":
		return llms.ImageURLContent{URL: part.URL, Detail: part.Detail}, nil
	case "binary":
		data, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil {
			return nil, err
		}
		return llms.BinaryContent{MIMEType: part.MIMEType, Data: data}, nil
	case "tool_call":
		toolCall := llms.ToolCall{ID: part.ToolCallID, Type: part.ToolType}
		if part.FunctionName != "" {
			toolCall.FunctionCall = &llms.FunctionCall{Name: part.FunctionName, Arguments: part.Arguments}
		}
		return toolCall, nil
	case "tool_response":
		return llms.ToolCallResponse{
			ToolCallID: part.ToolCallID,
			Name:       part.Name,
			Content:    part.Content,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported runner message part kind %q", part.Kind)
	}
}

func flattenSnapshot(snapshot StateSnapshot) (map[string]json.RawMessage, error) {
	result := make(map[string]json.RawMessage)

	rawRuntime, err := json.Marshal(snapshot.Runtime)
	if err != nil {
		return nil, err
	}
	result["runtime"] = rawRuntime

	for _, extension := range defaultStateExtensions() {
		if err := extension.AppendSnapshotFields(snapshot, result); err != nil {
			return nil, err
		}
	}

	for key, raw := range snapshot.Shared {
		result["shared."+key] = raw
	}

	for scopeName, scope := range snapshot.Scopes {
		for key, raw := range scope {
			result["scopes."+scopeName+"."+key] = raw
		}
	}

	for namespace, values := range snapshot.Internal {
		for key, raw := range values {
			result["internal."+namespace+"."+key] = raw
		}
	}

	if len(snapshot.Artifacts) > 0 {
		rawArtifacts, err := json.Marshal(snapshot.Artifacts)
		if err != nil {
			return nil, err
		}
		result["artifacts"] = rawArtifacts
	}

	return result, nil
}

func jsonEqual(left, right json.RawMessage) bool {
	return strings.TrimSpace(string(left)) == strings.TrimSpace(string(right))
}

func encodeGraphValue(key string, value any) (json.RawMessage, error) {
	if key == stateKeyMessages {
		messages, ok := value.([]llms.MessageContent)
		if !ok {
			return nil, fmt.Errorf("expected %q to be []llms.MessageContent, got %T", key, value)
		}
		serialized, err := serializeMessages(messages)
		if err != nil {
			return nil, err
		}
		return json.Marshal(serialized)
	}
	return json.Marshal(value)
}

func decodeGraphValue(key string, raw json.RawMessage) (any, error) {
	switch key {
	case stateKeyMessages:
		var messages []StateMessage
		if err := json.Unmarshal(raw, &messages); err != nil {
			return nil, err
		}
		return deserializeMessages(messages)
	case stateKeyFinalAnswer:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return value, nil
	case stateKeyIterationCount, stateKeyMaxIterations:
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return value, nil
	default:
		return decodeGenericGraphValue(raw)
	}
}

func decodeGenericGraphValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return normalizeJSONValue(value), nil
}

func normalizeJSONValue(value any) any {
	switch typed := value.(type) {
	case json.Number:
		text := typed.String()
		if strings.ContainsAny(text, ".eE") {
			floatValue, err := typed.Float64()
			if err != nil {
				return text
			}
			return floatValue
		}

		intValue, err := typed.Int64()
		if err != nil {
			floatValue, floatErr := typed.Float64()
			if floatErr != nil {
				return text
			}
			return floatValue
		}
		if intValue >= math.MinInt && intValue <= math.MaxInt {
			return int(intValue)
		}
		return intValue
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = normalizeJSONValue(item)
		}
		if str := normalizeStringSlice(items); str != nil {
			return str
		}
		if maps := normalizeMapSlice(items); maps != nil {
			return maps
		}
		return items
	case map[string]any:
		items := make(map[string]any, len(typed))
		for key, item := range typed {
			items[key] = normalizeJSONValue(item)
		}
		return items
	default:
		return value
	}
}

func normalizeStringSlice(values []any) []string {
	if len(values) == 0 {
		return nil
	}

	items := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil
		}
		items[i] = text
	}
	return items
}

func normalizeMapSlice(values []any) []map[string]any {
	if len(values) == 0 {
		return nil
	}

	items := make([]map[string]any, len(values))
	for i, value := range values {
		mapped, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		items[i] = mapped
	}
	return items
}

func isInternalSnapshotNamespaceKey(key string) bool {
	return strings.HasPrefix(strings.TrimSpace(key), stateNamespacePrefix) && !isInfrastructureStateKey(key)
}

func cloneArtifactRefs(artifacts []ArtifactRef) []ArtifactRef {
	if len(artifacts) == 0 {
		return nil
	}
	cloned := make([]ArtifactRef, len(artifacts))
	copy(cloned, artifacts)
	return cloned
}

func looksLikeLegacyScopeLayout(version string, scopes map[string]GraphState) bool {
	if version != "" && version != "v1" {
		return false
	}
	for _, scope := range scopes {
		if len(scope) == 0 {
			continue
		}
		hasLegacyKey := false
		for key := range scope {
			if key != "conversation" && key != "values" {
				return false
			}
			hasLegacyKey = true
		}
		if hasLegacyKey {
			return true
		}
	}
	return false
}

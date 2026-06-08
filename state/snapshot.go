package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/tmc/langchaingo/llms"
)

const DefaultSnapshotVersion = "state-v2"

type StateSnapshot struct {
	Version  string         `json:"version"`
	Shared   map[string]any `json:"shared,omitempty"`
	Scopes   map[string]any `json:"scopes,omitempty"`
	Internal map[string]any `json:"internal,omitempty"`
	Runtime  map[string]any `json:"runtime,omitempty"`
}

type StateChange struct {
	Path   string `json:"path"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

type JSONStateCodec struct {
	version string
}

func NewJSONStateCodec(version string) *JSONStateCodec {
	version = normalizeSegment(version)
	if version == "" {
		version = DefaultSnapshotVersion
	}
	return &JSONStateCodec{version: version}
}

func (c *JSONStateCodec) Name() string {
	return "json"
}

func (c *JSONStateCodec) Version() string {
	if c == nil || c.version == "" {
		return DefaultSnapshotVersion
	}
	return c.version
}

func (c *JSONStateCodec) Encode(snapshot StateSnapshot) ([]byte, error) {
	if snapshot.Version == "" {
		snapshot.Version = c.Version()
	}
	return json.Marshal(snapshot)
}

func (c *JSONStateCodec) Decode(data []byte) (StateSnapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var snapshot StateSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return StateSnapshot{}, err
	}
	if snapshot.Version == "" {
		snapshot.Version = c.Version()
	}
	snapshot.Shared = normalizeDecodedMap(snapshot.Shared)
	snapshot.Scopes = normalizeDecodedMap(snapshot.Scopes)
	snapshot.Internal = normalizeDecodedMap(snapshot.Internal)
	snapshot.Runtime = normalizeDecodedMap(snapshot.Runtime)
	return DecodeSnapshotMessages(snapshot)
}

func (c *JSONStateCodec) Diff(before, after StateSnapshot) ([]StateChange, error) {
	return DiffSnapshots(before, after)
}

func SnapshotFromState(state *State) (StateSnapshot, error) {
	root := NewState().Export()
	if state != nil {
		root = state.Export()
	}

	shared, err := encodeSnapshotSection(root[SectionShared])
	if err != nil {
		return StateSnapshot{}, fmt.Errorf("encode shared state: %w", err)
	}
	scopes, err := encodeSnapshotSection(root[SectionScopes])
	if err != nil {
		return StateSnapshot{}, fmt.Errorf("encode scoped state: %w", err)
	}
	internal, err := encodeSnapshotSection(root[SectionInternal])
	if err != nil {
		return StateSnapshot{}, fmt.Errorf("encode internal state: %w", err)
	}
	runtime, err := encodeSnapshotSection(root[SectionRuntime])
	if err != nil {
		return StateSnapshot{}, fmt.Errorf("encode runtime state: %w", err)
	}

	return StateSnapshot{
		Version:  DefaultSnapshotVersion,
		Shared:   emptyMapToNil(shared),
		Scopes:   emptyMapToNil(scopes),
		Internal: emptyMapToNil(internal),
		Runtime:  emptyMapToNil(runtime),
	}, nil
}

func StateFromSnapshot(snapshot StateSnapshot) (*State, error) {
	decoded, err := DecodeSnapshotMessages(snapshot)
	if err != nil {
		return nil, err
	}
	return FromMap(map[string]any{
		SectionShared:   emptyMapToEmpty(decoded.Shared),
		SectionScopes:   emptyMapToEmpty(decoded.Scopes),
		SectionInternal: emptyMapToEmpty(decoded.Internal),
		SectionRuntime:  emptyMapToEmpty(decoded.Runtime),
	}), nil
}

func DecodeSnapshotMessages(snapshot StateSnapshot) (StateSnapshot, error) {
	shared, err := decodeSnapshotSectionMessages(SectionShared, snapshot.Shared)
	if err != nil {
		return StateSnapshot{}, err
	}
	scopes, err := decodeSnapshotSectionMessages(SectionScopes, snapshot.Scopes)
	if err != nil {
		return StateSnapshot{}, err
	}
	snapshot.Shared = shared
	snapshot.Scopes = scopes
	return snapshot, nil
}

func DiffSnapshots(before, after StateSnapshot) ([]StateChange, error) {
	beforeFlat, err := flattenSnapshot(before)
	if err != nil {
		return nil, err
	}
	afterFlat, err := flattenSnapshot(after)
	if err != nil {
		return nil, err
	}

	paths := make(map[string]struct{}, len(beforeFlat)+len(afterFlat))
	for path := range beforeFlat {
		paths[path] = struct{}{}
	}
	for path := range afterFlat {
		paths[path] = struct{}{}
	}

	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)

	changes := make([]StateChange, 0)
	for _, path := range ordered {
		beforeValue, beforeOK := beforeFlat[path]
		afterValue, afterOK := afterFlat[path]
		if beforeOK && afterOK && jsonEqual(beforeValue, afterValue) {
			continue
		}
		change := StateChange{Path: path}
		if beforeOK {
			change.Before = cloneValue(beforeValue)
		}
		if afterOK {
			change.After = cloneValue(afterValue)
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func encodeSnapshotSection(value any) (map[string]any, error) {
	mapped, ok := asMap(value)
	if !ok || mapped == nil {
		return map[string]any{}, nil
	}
	encoded, err := encodeSnapshotValue("", cloneMap(mapped))
	if err != nil {
		return nil, err
	}
	section, ok := encoded.(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	return section, nil
}

func encodeSnapshotValue(path string, value any) (any, error) {
	if isConversationMessagesPath(path) {
		switch typed := value.(type) {
		case []llms.MessageContent:
			return SerializeMessages(typed)
		case []StateMessage:
			return cloneValue(typed), nil
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			nextPath := key
			if path != "" {
				nextPath = path + "." + key
			}
			encoded, err := encodeSnapshotValue(nextPath, item)
			if err != nil {
				return nil, err
			}
			result[key] = encoded
		}
		return result, nil
	default:
		return cloneValue(value), nil
	}
}

func decodeSnapshotSectionMessages(section string, values map[string]any) (map[string]any, error) {
	if len(values) == 0 {
		return values, nil
	}
	decoded, err := decodeSnapshotMessagesValue(section, cloneMap(values))
	if err != nil {
		return nil, err
	}
	mapped, ok := decoded.(map[string]any)
	if !ok {
		return values, nil
	}
	return mapped, nil
}

func decodeSnapshotMessagesValue(path string, value any) (any, error) {
	if isConversationMessagesPath(path) {
		messages, err := decodeStateMessagesValue(value)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		return messages, nil
	}

	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			nextPath := key
			if path != "" {
				nextPath = path + "." + key
			}
			decoded, err := decodeSnapshotMessagesValue(nextPath, item)
			if err != nil {
				return nil, err
			}
			result[key] = decoded
		}
		return result, nil
	default:
		return cloneValue(value), nil
	}
}

func decodeStateMessagesValue(value any) ([]llms.MessageContent, error) {
	switch typed := value.(type) {
	case []llms.MessageContent:
		return cloneMessagesForSnapshot(typed), nil
	case []StateMessage:
		return DeserializeMessages(typed)
	case []any:
		messages := make([]StateMessage, 0, len(typed))
		for _, item := range typed {
			messageMap, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected message object, got %T", item)
			}
			message, err := stateMessageFromMap(messageMap)
			if err != nil {
				return nil, err
			}
			messages = append(messages, message)
		}
		return DeserializeMessages(messages)
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("expected messages slice, got %T", value)
	}
}

func stateMessageFromMap(values map[string]any) (StateMessage, error) {
	message := StateMessage{}
	if role, ok := values["role"].(string); ok {
		message.Role = role
	}
	if rawParts, ok := values["parts"].([]any); ok {
		for _, rawPart := range rawParts {
			partMap, ok := rawPart.(map[string]any)
			if !ok {
				return StateMessage{}, fmt.Errorf("expected message part object, got %T", rawPart)
			}
			message.Parts = append(message.Parts, stateMessagePartFromMap(partMap))
		}
	}
	return message, nil
}

func stateMessagePartFromMap(values map[string]any) StateMessagePart {
	return StateMessagePart{
		Kind:         stringField(values, "kind"),
		Text:         stringField(values, "text"),
		URL:          stringField(values, "url"),
		Detail:       stringField(values, "detail"),
		MIMEType:     stringField(values, "mime_type"),
		Data:         stringField(values, "data"),
		ToolCallID:   stringField(values, "tool_call_id"),
		ToolType:     stringField(values, "tool_type"),
		FunctionName: stringField(values, "function_name"),
		Arguments:    stringField(values, "arguments"),
		Name:         stringField(values, "name"),
		Content:      stringField(values, "content"),
	}
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func isConversationMessagesPath(path string) bool {
	if path == "conversation.messages" {
		return true
	}
	return len(path) > len(".conversation.messages") &&
		path[len(path)-len(".conversation.messages"):] == ".conversation.messages"
}

func flattenSnapshot(snapshot StateSnapshot) (map[string]any, error) {
	values := map[string]any{}
	for _, section := range []struct {
		name  string
		value map[string]any
	}{
		{name: SectionShared, value: snapshot.Shared},
		{name: SectionScopes, value: snapshot.Scopes},
		{name: SectionInternal, value: snapshot.Internal},
		{name: SectionRuntime, value: snapshot.Runtime},
	} {
		if len(section.value) == 0 {
			continue
		}
		if err := flattenValue(values, section.name, section.value); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func flattenValue(output map[string]any, path string, value any) error {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 {
			output[path] = map[string]any{}
			return nil
		}
		for key, item := range typed {
			nextPath := key
			if path != "" {
				nextPath = path + "." + key
			}
			if err := flattenValue(output, nextPath, item); err != nil {
				return err
			}
		}
	default:
		output[path] = cloneValue(value)
	}
	return nil
}

func jsonEqual(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func normalizeDecodedMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return values
	}
	normalized, _ := normalizeDecodedValue(values).(map[string]any)
	return normalized
}

func normalizeDecodedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = normalizeDecodedValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = normalizeDecodedValue(item)
		}
		return result
	case json.Number:
		text := string(typed)
		if integer, err := strconv.ParseInt(text, 10, 64); err == nil {
			if integer >= minIntValue() && integer <= maxIntValue() {
				return int(integer)
			}
			return integer
		}
		if number, err := strconv.ParseFloat(text, 64); err == nil {
			return number
		}
		return text
	default:
		return value
	}
}

func maxIntValue() int64 {
	return int64(^uint(0) >> 1)
}

func minIntValue() int64 {
	return -maxIntValue() - 1
}

func emptyMapToNil(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	return values
}

func emptyMapToEmpty(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	return values
}

func cloneMessagesForSnapshot(messages []llms.MessageContent) []llms.MessageContent {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]llms.MessageContent, len(messages))
	for i, message := range messages {
		cloned[i] = llms.MessageContent{
			Role:  message.Role,
			Parts: append([]llms.ContentPart(nil), message.Parts...),
		}
	}
	return cloned
}

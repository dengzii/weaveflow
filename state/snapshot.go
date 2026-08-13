package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
)

const DefaultSnapshotVersion = "state-v2"

type Snapshot struct {
	Version  string         `json:"version"`
	Shared   map[string]any `json:"shared,omitempty"`
	Scopes   map[string]any `json:"scopes,omitempty"`
	Internal map[string]any `json:"internal,omitempty"`
	Runtime  map[string]any `json:"runtime,omitempty"`
}

type Change struct {
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

func (c *JSONStateCodec) Encode(snapshot Snapshot) ([]byte, error) {
	if snapshot.Version == "" {
		return nil, fmt.Errorf("state snapshot version is required")
	}
	if snapshot.Version != c.Version() {
		return nil, fmt.Errorf("state snapshot version %q does not match codec version %q", snapshot.Version, c.Version())
	}
	return json.Marshal(snapshot)
}

func (c *JSONStateCodec) Decode(data []byte) (Snapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()

	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Snapshot{}, fmt.Errorf("state snapshot contains multiple JSON values")
		}
		return Snapshot{}, err
	}
	if snapshot.Version == "" {
		return Snapshot{}, fmt.Errorf("state snapshot version is required")
	}
	if snapshot.Version != c.Version() {
		return Snapshot{}, fmt.Errorf("state snapshot version %q does not match codec version %q", snapshot.Version, c.Version())
	}
	snapshot.Shared = normalizeDecodedMap(snapshot.Shared)
	snapshot.Scopes = normalizeDecodedMap(snapshot.Scopes)
	snapshot.Internal = normalizeDecodedMap(snapshot.Internal)
	snapshot.Runtime = normalizeDecodedMap(snapshot.Runtime)
	return snapshot, nil
}

func (c *JSONStateCodec) Diff(before, after Snapshot) ([]Change, error) {
	return DiffSnapshots(before, after)
}

func SnapshotFromState(current *State) (Snapshot, error) {
	root := NewState().Export()
	if current != nil {
		root = current.Export()
	}

	shared, err := encodeSnapshotSection(root[SectionShared])
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode shared state: %w", err)
	}
	scopes, err := encodeSnapshotSection(root[SectionScopes])
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode scoped state: %w", err)
	}
	internal, err := encodeSnapshotSection(root[SectionInternal])
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode internal state: %w", err)
	}
	runtime, err := encodeSnapshotSection(root[SectionRuntime])
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode runtime state: %w", err)
	}

	return Snapshot{
		Version:  DefaultSnapshotVersion,
		Shared:   emptyMapToNil(shared),
		Scopes:   emptyMapToNil(scopes),
		Internal: emptyMapToNil(internal),
		Runtime:  emptyMapToNil(runtime),
	}, nil
}

func FromSnapshot(snapshot Snapshot) (*State, error) {
	return FromMap(map[string]any{
		SectionShared:   emptyMapToEmpty(snapshot.Shared),
		SectionScopes:   emptyMapToEmpty(snapshot.Scopes),
		SectionInternal: emptyMapToEmpty(snapshot.Internal),
		SectionRuntime:  emptyMapToEmpty(snapshot.Runtime),
	}), nil
}

func DiffSnapshots(before, after Snapshot) ([]Change, error) {
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

	changes := make([]Change, 0)
	for _, path := range ordered {
		beforeValue, beforeOK := beforeFlat[path]
		afterValue, afterOK := afterFlat[path]
		if beforeOK && afterOK && jsonEqual(beforeValue, afterValue) {
			continue
		}
		change := Change{Path: path}
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
	encoded, err := encodeSnapshotValue(cloneMap(mapped))
	if err != nil {
		return nil, err
	}
	section, ok := encoded.(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	return section, nil
}

func encodeSnapshotValue(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			encoded, err := encodeSnapshotValue(item)
			if err != nil {
				return nil, err
			}
			result[key] = encoded
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			encoded, err := encodeSnapshotValue(item)
			if err != nil {
				return nil, err
			}
			result[index] = encoded
		}
		return result, nil
	default:
		payload, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		var encoded any
		if err := decoder.Decode(&encoded); err != nil {
			return nil, err
		}
		return normalizeDecodedValue(encoded), nil
	}
}

func flattenSnapshot(snapshot Snapshot) (map[string]any, error) {
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

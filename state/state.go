// Package state provides structured graph state, paths, contracts, patches, and snapshots.
package state

import (
	"encoding/json"
	"fmt"
)

// State is the private storage envelope for state.
// External packages should use Access, typed Ref values, or registered
// capability views instead of mutating this structure directly.
type State struct {
	root map[string]any
}

// NewState creates an empty state envelope with all root sections present.
func NewState() *State {
	return &State{root: newRoot()}
}

// FromMap creates state from an exported envelope. Missing root sections are
// initialized.
func FromMap(input map[string]any) *State {
	result := NewState()
	if input != nil {
		mergeMap(result.root, input)
	}
	result.ensureRootSections()
	return result
}

// FromShared creates state with values placed under the shared section.
func FromShared(shared map[string]any) *State {
	result := NewState()
	if shared != nil {
		section, _ := result.root[SectionShared].(map[string]any)
		mergeMap(section, shared)
	}
	return result
}

// Clone returns a deep copy of state.
func (s *State) Clone() *State {
	if s == nil {
		return NewState()
	}
	return FromMap(s.root)
}

// Export returns a deep copy of the state envelope.
func (s *State) Export() map[string]any {
	if s == nil {
		return newRoot()
	}
	return cloneMap(s.root)
}

// MarshalJSON encodes the exported state envelope.
func (s *State) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Export())
}

// UnmarshalJSON decodes an exported state envelope and initializes missing
// root sections.
func (s *State) UnmarshalJSON(data []byte) error {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	*s = *FromMap(root)
	return nil
}

// SetSection replaces one root section. The value must be an object.
func (s *State) SetSection(section string, values map[string]any) error {
	path, err := NewPath(section)
	if err != nil {
		return err
	}
	return s.set(path, values)
}

func (s *State) read(path Path) (any, bool) {
	if s == nil || path.Empty() {
		return nil, false
	}
	current, ok := s.root[path.section]
	if !ok {
		return nil, false
	}
	for _, segment := range path.segments {
		mapped, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = mapped[segment]
		if !ok {
			return nil, false
		}
	}
	return cloneValue(current), true
}

func (s *State) set(path Path, value any) error {
	if s == nil {
		return fmt.Errorf("state is nil")
	}
	if path.Empty() {
		return fmt.Errorf("state path is required")
	}
	if len(path.segments) == 0 {
		mapped, ok := asMap(value)
		if !ok {
			return fmt.Errorf("state section %q requires map[string]any value", path.section)
		}
		s.root[path.section] = cloneMap(mapped)
		return nil
	}
	parent, key, err := s.parentMap(path, true)
	if err != nil {
		return err
	}
	parent[key] = cloneValue(value)
	return nil
}

func (s *State) delete(path Path) error {
	if s == nil {
		return fmt.Errorf("state is nil")
	}
	if path.Empty() {
		return fmt.Errorf("state path is required")
	}
	if len(path.segments) == 0 {
		s.root[path.section] = map[string]any{}
		return nil
	}
	parent, key, err := s.parentMap(path, false)
	if err != nil || parent == nil || key == "" {
		return err
	}
	delete(parent, key)
	return nil
}

func (s *State) merge(path Path, value any) error {
	if s == nil {
		return fmt.Errorf("state is nil")
	}
	if path.Empty() {
		return fmt.Errorf("state path is required")
	}
	overlay, ok := asMap(value)
	if !ok {
		return fmt.Errorf("merge at %q requires map[string]any value, got %T", path.String(), value)
	}
	current, ok := s.read(path)
	if !ok {
		return s.set(path, overlay)
	}
	target, ok := asMap(current)
	if !ok {
		return fmt.Errorf("merge at %q found non-object value %T", path.String(), current)
	}
	mergeMap(target, overlay)
	return s.set(path, target)
}

func (s *State) parentMap(path Path, create bool) (map[string]any, string, error) {
	if path.Empty() {
		return nil, "", fmt.Errorf("state path is required")
	}
	s.ensureRootSections()
	current, ok := s.root[path.section].(map[string]any)
	if !ok {
		if !create {
			return nil, "", nil
		}
		current = map[string]any{}
		s.root[path.section] = current
	}
	if len(path.segments) == 0 {
		return s.root, path.section, nil
	}
	for _, segment := range path.segments[:len(path.segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			if !create {
				return nil, "", nil
			}
			next = map[string]any{}
			current[segment] = next
		}
		current = next
	}
	return current, path.segments[len(path.segments)-1], nil
}

func (s *State) ensureRootSections() {
	if s.root == nil {
		s.root = newRoot()
		return
	}
	for _, section := range []string{SectionShared, SectionScopes, SectionInternal, SectionRuntime} {
		if _, ok := s.root[section].(map[string]any); !ok {
			s.root[section] = map[string]any{}
		}
	}
}

func newRoot() map[string]any {
	return map[string]any{
		SectionShared:   map[string]any{},
		SectionScopes:   map[string]any{},
		SectionInternal: map[string]any{},
		SectionRuntime:  map[string]any{},
	}
}

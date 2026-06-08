package state

import (
	"encoding/json"
	"fmt"
)

// State is the private storage envelope for state.
// External packages should use Access, typed Ref values, or registered
// accessors instead of mutating this structure directly.
type State struct {
	root map[string]any
}

func NewState() *State {
	return &State{root: newRoot()}
}

func FromMap(input map[string]any) *State {
	state := NewState()
	if input != nil {
		mergeMap(state.root, input)
	}
	state.ensureRootSections()
	return state
}

func FromShared(shared map[string]any) *State {
	state := NewState()
	if shared != nil {
		section, _ := state.root[SectionShared].(map[string]any)
		mergeMap(section, shared)
	}
	return state
}

func (s *State) Clone() *State {
	if s == nil {
		return NewState()
	}
	return FromMap(s.root)
}

func (s *State) Export() map[string]any {
	if s == nil {
		return newRoot()
	}
	return cloneMap(s.root)
}

func (s *State) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Export())
}

func (s *State) UnmarshalJSON(data []byte) error {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	*s = *FromMap(root)
	return nil
}

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

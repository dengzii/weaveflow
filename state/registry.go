package state

import "fmt"

type AccessorID[T any] struct {
	name string
}

func NewAccessorID[T any](name string) AccessorID[T] {
	return AccessorID[T]{name: normalizeSegment(name)}
}

func (id AccessorID[T]) Name() string {
	return id.name
}

type AccessorFactory func(access *Access) any
type ContractFactory func(scope string) Contract

type AccessorDefinition struct {
	Name            string
	Contract        Contract
	ContractFactory ContractFactory
	Factory         AccessorFactory
}

type Extension interface {
	Name() string
	Install(registry *Registry) error
}

type Registry struct {
	accessors map[string]AccessorDefinition
}

func NewRegistry() *Registry {
	return &Registry{accessors: map[string]AccessorDefinition{}}
}

func (r *Registry) RegisterAccessor(def AccessorDefinition) error {
	if r == nil {
		return fmt.Errorf("state accessor registry is nil")
	}
	def.Name = normalizeSegment(def.Name)
	if def.Name == "" {
		return fmt.Errorf("state accessor name is required")
	}
	if def.Factory == nil {
		return fmt.Errorf("state accessor %q factory is required", def.Name)
	}
	if _, exists := r.accessors[def.Name]; exists {
		return fmt.Errorf("state accessor %q already registered", def.Name)
	}
	def.Contract = def.Contract.Clone()
	r.accessors[def.Name] = def
	return nil
}

func (r *Registry) Install(extension Extension) error {
	if r == nil {
		return fmt.Errorf("state accessor registry is nil")
	}
	if extension == nil {
		return fmt.Errorf("state extension is nil")
	}
	if normalizeSegment(extension.Name()) == "" {
		return fmt.Errorf("state extension name is required")
	}
	return extension.Install(r)
}

func (r *Registry) AccessorContract(name string, scope ...string) (Contract, bool) {
	if r == nil {
		return Contract{}, false
	}
	def, ok := r.accessors[normalizeSegment(name)]
	if !ok {
		return Contract{}, false
	}
	if def.ContractFactory != nil {
		selectedScope := ""
		if len(scope) > 0 {
			selectedScope = normalizeSegment(scope[0])
		}
		return def.ContractFactory(selectedScope).Clone(), true
	}
	return def.Contract.Clone(), true
}

func UseAccessor[T any](access *Access, id AccessorID[T]) (T, error) {
	var zero T
	if access == nil {
		return zero, fmt.Errorf("state access is nil")
	}
	name := normalizeSegment(id.Name())
	if name == "" {
		return zero, fmt.Errorf("state accessor id is required")
	}
	def, ok := access.Registry().accessors[name]
	if !ok {
		return zero, fmt.Errorf("state accessor %q is not registered", name)
	}
	facet := def.Factory(access)
	typed, ok := facet.(T)
	if !ok {
		return zero, fmt.Errorf("state accessor %q returned %T, not %s", name, facet, typeName[T]())
	}
	return typed, nil
}

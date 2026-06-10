package node

import (
	"fmt"
	"strings"

	"weaveflow/core"
	"weaveflow/state"
)

type Node interface {
	ID() string
	Name() string
	Description() string
	Scope() string
	AccessorUses() []AccessorUse
	Execute(ctx core.Context, access *state.Access) error
}

type AccessorUse struct {
	Name             string
	Scope            string
	InheritNodeScope bool
}

func Use(accessorName string) AccessorUse {
	return AccessorUse{Name: accessorName, InheritNodeScope: true}
}

func UseRoot(accessorName string) AccessorUse {
	return AccessorUse{Name: accessorName}
}

func UseScoped(accessorName string, scope string) AccessorUse {
	return AccessorUse{Name: accessorName, Scope: scope}
}

func (u AccessorUse) EffectiveScope(nodeScope string) string {
	if u.InheritNodeScope {
		return nodeScope
	}
	return u.Scope
}

type Spec struct {
	ID           string
	Name         string
	Description  string
	Scope        string
	AccessorUses []AccessorUse
}

func (s Spec) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("node spec id is required")
	}
	return nil
}

type Base struct {
	Spec Spec
}

type NodeOption func(*Base)

func WithID(id string) NodeOption {
	return func(base *Base) {
		if base != nil {
			base.SetID(id)
		}
	}
}

func WithName(name string) NodeOption {
	return func(base *Base) {
		if base != nil {
			base.Spec.Name = strings.TrimSpace(name)
		}
	}
}

func WithScope(scope string) NodeOption {
	return func(base *Base) {
		if base != nil {
			base.Spec.Scope = strings.TrimSpace(scope)
		}
	}
}

func NewBase(spec Spec) Base {
	return Base{Spec: spec}
}

func applyNodeOptions(base *Base, options []NodeOption) {
	for _, option := range options {
		if option != nil {
			option(base)
		}
	}
}

func (b *Base) Validate() error {
	return b.Spec.Validate()
}

func (b *Base) ID() string {
	return b.Spec.ID
}

func (b *Base) SetID(id string) {
	if b == nil {
		return
	}
	b.Spec.ID = strings.TrimSpace(id)
	if b.Spec.ID != "" {
		b.Spec.Name = b.Spec.ID
	}
}

func (b *Base) Name() string {
	if b.Spec.Name != "" {
		return b.Spec.Name
	}
	return b.Spec.ID
}

func (b *Base) Description() string {
	return b.Spec.Description
}

func (b *Base) Scope() string {
	return b.Spec.Scope
}

func (b *Base) AccessorUses() []AccessorUse {
	if len(b.Spec.AccessorUses) == 0 {
		return nil
	}
	return append([]AccessorUse(nil), b.Spec.AccessorUses...)
}

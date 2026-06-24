package registry

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
)

func (r *Registry) RegisterStateField(def dsl.StateFieldDefinition) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if r.StateFields == nil {
		r.StateFields = map[string]dsl.StateFieldDefinition{}
	}
	def.Name = strings.TrimSpace(def.Name)
	if def.Name == "" {
		return fmt.Errorf("state field name is required")
	}
	if _, exists := r.StateFields[def.Name]; exists {
		return fmt.Errorf("state field %q is already registered", def.Name)
	}
	r.StateFields[def.Name] = def
	return nil
}

func (r *Registry) RegisterNodeType(def NodeTypeDefinition) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if r.NodeTypes == nil {
		r.NodeTypes = map[string]NodeTypeDefinition{}
	}
	def.Type = strings.TrimSpace(def.Type)
	if def.Type == "" {
		return fmt.Errorf("node type is required")
	}
	if def.Build == nil {
		return fmt.Errorf("node type %q builder is required", def.Type)
	}
	if _, exists := r.NodeTypes[def.Type]; exists {
		return fmt.Errorf("node type %q is already registered", def.Type)
	}
	r.NodeTypes[def.Type] = def
	return nil
}

func (r *Registry) RegisterCondition(def ConditionDefinition) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if r.Conditions == nil {
		r.Conditions = map[string]ConditionDefinition{}
	}
	def.Type = strings.TrimSpace(def.Type)
	if def.Type == "" {
		return fmt.Errorf("condition type is required")
	}
	if def.Resolve == nil {
		return fmt.Errorf("condition %q resolver is required", def.Type)
	}
	if _, exists := r.Conditions[def.Type]; exists {
		return fmt.Errorf("condition %q is already registered", def.Type)
	}
	r.Conditions[def.Type] = def
	return nil
}

func (r *Registry) ResolveCondition(spec dsl.GraphConditionSpec) (EdgeCondition, error) {
	if r == nil {
		return EdgeCondition{}, fmt.Errorf("registry is nil")
	}
	spec = dsl.NormalizeGraphConditionSpec(spec)
	if spec.Type == "" {
		return EdgeCondition{}, fmt.Errorf("condition type is required")
	}
	conditionDef, ok := r.Conditions[spec.Type]
	if !ok {
		return EdgeCondition{}, fmt.Errorf("condition %q is not registered", spec.Type)
	}
	condition, err := conditionDef.Resolve(spec)
	if err != nil {
		return EdgeCondition{}, err
	}
	return condition.WithSpec(spec), nil
}

func (r *Registry) ResolveNodeStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	if r == nil {
		return dsl.StateContract{}, fmt.Errorf("registry is nil")
	}
	spec = dsl.NormalizeGraphDefinition(dsl.GraphDefinition{Nodes: []dsl.GraphNodeSpec{spec}}).Nodes[0]
	if spec.Type == "" {
		return dsl.StateContract{}, fmt.Errorf("node type is required")
	}
	nodeDef, ok := r.NodeTypes[spec.Type]
	if !ok {
		return dsl.StateContract{}, fmt.Errorf("node type %q is not registered", spec.Type)
	}
	if nodeDef.ResolveStateContract != nil {
		contract, err := nodeDef.ResolveStateContract(spec)
		if err != nil {
			return dsl.StateContract{}, err
		}
		return contract.Clone(), nil
	}
	if nodeDef.StateContract == nil {
		return dsl.StateContract{}, nil
	}
	return nodeDef.StateContract.Clone(), nil
}

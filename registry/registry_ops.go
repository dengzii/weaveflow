package registry

import (
	"fmt"

	"weaveflow/dsl"
)

func (r *Registry) RegisterStateField(def dsl.StateFieldDefinition) {
	r.StateFields[def.Name] = def
}

func (r *Registry) RegisterNodeType(def NodeTypeDefinition) {
	r.NodeTypes[def.Type] = def
}

func (r *Registry) RegisterCondition(def ConditionDefinition) {
	r.Conditions[def.Type] = def
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

# Build Custom Nodes

Custom nodes let an application keep domain logic inside the graph while preserving the validation and inspection
contracts used by built-ins. A custom node has two parts: a runtime implementation and a registry definition.

## 1. Implement the node

The smallest implementation embeds `node.Base` and implements `core.Node` through `Execute`:

```go
type NormalizeNode struct {
    node.Base
    InputPath  state.Path
    OutputPath state.Path
}

func (n *NormalizeNode) Execute(_ core.Context, access *state.Access) (core.NodeResult, error) {
    value, ok := access.ReadAny(n.InputPath)
    if !ok {
        return core.NodeResult{}, fmt.Errorf("input is missing")
    }
    text, ok := value.(string)
    if !ok {
        return core.NodeResult{}, fmt.Errorf("expected string input")
    }
    return core.NodeResult{Patch: state.NewPatch(state.PatchOp{
        Kind: state.OpSet, Path: n.OutputPath, Value: strings.TrimSpace(text),
    })}, nil
}
```

Use the access object for state reads and writes. Do not reach into the whole snapshot or use an unbound path. If the
operation has external side effects, make its idempotency and effect-resolution behavior explicit before allowing
automatic retries.

## 2. Declare the contract

Register a stable type with a schema and State Ports. The builder receives resolved paths only after the graph has passed
contract validation:

```go
registry.NodeTypeDefinition{
    NodeTypeSchema: dsl.NodeTypeSchema{
        Type: "normalize_text",
        Title: "Normalize text",
        ConfigSchema: dsl.JSONSchema{"type": "object", "additionalProperties": false},
    },
    StatePorts: []dsl.StatePortDefinition{
        {Name: "input", Required: true, Mode: dsl.StateAccessRead,
            Schema: dsl.JSONSchema{"type": "string"}},
        {Name: "output", Required: true, Mode: dsl.StateAccessWrite,
            Schema: dsl.JSONSchema{"type": "string"}},
    },
    Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
        input, ok := resolved.State["input"]
        if !ok {
            return nil, fmt.Errorf("input binding is required")
        }
        output, ok := resolved.State["output"]
        if !ok {
            return nil, fmt.Errorf("output binding is required")
        }
        return &NormalizeNode{
            Base: node.NewBase(node.Spec{ID: resolved.Spec.ID, Name: resolved.Spec.Name}),
            InputPath: input.Path,
            OutputPath: output.Path,
        }, nil
    },
}
```

The example shows the shape of a definition; adapt `Base` construction and patch helpers to the package version you use.
The important invariant is that every path is a named port and the builder consumes resolved bindings.

## 3. Register and build

```go
reg := builtin.NewDefaultRegistry()
if err := reg.RegisterNodeType(normalizeDefinition()); err != nil {
    log.Fatal(err)
}
graph, err := weaveflow.BuildGraph(reg, definition)
if err != nil {
    log.Fatal(err)
}
```

Use a unique, versioned type name if the contract may change. Existing Graph Definitions reference the registered type
string, so changing port names or access modes is a compatibility change.

## Contract checklist

- Give every port a schema, access mode, and clear description.
- Mark required ports and validate optional ports in the builder.
- Use a capability contract for structured roots such as conversations or plans.
- Declare a reducer when parallel branches intentionally share a destination.
- Add focused tests for registry validation, graph building, state behavior, and failure classification.
- Export the registry schema with `reg.JSONSchema()` for editor integration.

Conditions and reducers use the same registry pattern. See the [Package Map](/reference/packages) for the package owning
each contract.

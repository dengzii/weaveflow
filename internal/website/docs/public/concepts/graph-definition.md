# Graph Definition

A Graph Definition is the serializable source of truth for a workflow's topology and component configuration. It declares
the graph version, State Modules, nodes, edges, and entry point.

```json
{
  "version": "1.0",
  "name": "assistant",
  "state_modules": [
    { "name": "weaveflow.protocols", "version": "1" }
  ],
  "entry_point": "input",
  "nodes": [
    {
      "id": "input",
      "type": "user_input",
      "state": {
        "value": { "path": "shared.request.input" }
      }
    }
  ],
  "edges": []
}
```

## Top-level fields

| Field | Meaning |
| --- | --- |
| `version` | Definition schema version. The current value is `1.0`. |
| `name`, `description` | Human-readable name and description shown by the Workbench. |
| `state_modules` | Versioned modules that provide fields and capabilities. |
| `entry_point` | Node where execution starts. |
| `finish_point` | Optional explicit finish node. |
| `nodes` | Registered executable components. |
| `edges` | Normal, conditional, and failure routes. |
| `policy` | Execution limits and behavior for this graph. |
| `metadata` | Application-owned JSON metadata. |

`nodes` and `state_modules` are required by validation. Node IDs must be unique, every edge source must name a node, and
every edge target must resolve to a node or the reserved `__end__` reference.

## Nodes

Each node has a stable ID and a registered type. Component settings belong in `config`; State Port bindings belong in
`state`. Keeping these sections separate lets the registry validate data access before a Run starts.

## Edges and conditions

Normal edges connect one node to another. A condition belongs to an edge and determines whether that path is available; it
is not a standalone node.

Use normal edges for unconditional sequencing and conditional edges for routing, tool-call loops, approval decisions,
and failure fallback paths.

An edge can contain one condition or one failure route. A conditional edge is available when its resolver returns true;
failure routes select an alternate target after a matching node or condition error. Keep fallback edges separate from the
normal success path so the graph's intent stays clear.

## Execution policy

Use the optional `policy` object for graph-level limits such as execution time, state size, concurrency, and retry or
checkpoint behavior. Policies are part of the definition hash and are captured by a Graph Session. Start with conservative
limits and increase them only after inspecting real Run evidence.

## Immutable sessions

The Debug Server stores a Graph and creates immutable Graph Sessions for execution. Runtime settings are captured with the
Session, so a Run can be traced back to the exact definition and configuration that produced it.

## Validation

Before execution, the registry and graph builder validate component types, State Modules, State Port bindings, entry
points, edge targets, required producers, and conflicting parallel writes.

Validate a complete definition in code before writing it to disk:

```go
if err := definition.Validate(); err != nil {
    log.Fatal(err)
}
graph, err := weaveflow.BuildGraph(builtin.NewDefaultRegistry(), definition)
if err != nil {
    log.Fatal(err)
}
```

The registry-aware build catches errors that shape validation alone cannot, such as an unknown node type, a capability
contract mismatch, an unsupported reducer, or a missing required binding.

## Versioning workflow

Treat a definition and its registry as a pair. Store JSON in source control, review topology and state changes together,
and create a new Graph Session after every behavior change. Existing Sessions remain immutable, so historical Runs stay
traceable to the exact definition and settings that created them.

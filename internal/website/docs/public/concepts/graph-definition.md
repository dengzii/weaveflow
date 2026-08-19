# Graph Definition

A Graph Definition is the serializable source of truth for topology and component configuration. It declares the graph
version, State Modules, nodes, edges, and entry point.

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

## Nodes

Each node has a stable ID and registered type. Component settings belong in `config`; State Port bindings belong in
`state`. Keeping these sections separate allows the registry to validate access before a run starts.

## Edges and conditions

Normal edges connect one node to another. A condition belongs to an edge and selects whether that path is available; it
is not represented as a standalone node.

Use normal edges for unconditional sequencing and conditional edges for routing, tool-call loops, approval decisions,
and failure fallback paths.

## Immutable sessions

The debug server stores a Graph and creates immutable Graph Sessions for execution. Runtime settings are captured with
the Session so a Run can be traced back to the exact definition and configuration that produced it.

## Validation

Before execution, the registry and graph builder validate component types, State Modules, State Port bindings, entry
points, edge targets, required producers, and conflicting parallel writes.

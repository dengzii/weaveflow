# State Bindings

State Bindings connect a component's declared State Ports to concrete state paths. They make data access explicit, so
invalid reads, writes, and parallel merge behavior can be rejected before execution.

```json
{
  "id": "llm",
  "type": "llm_turn",
  "config": {
    "tool_ids": ["calculator"]
  },
  "state": {
    "conversation": {
      "path": "scopes.llm.conversation"
    },
    "output": {
      "path": "shared.final.answer"
    }
  }
}
```

## State roots

- `shared.*` contains graph-wide data intentionally shared across nodes.
- `scopes.<node>.*` isolates node- or capability-owned state.

External Run input may initialize `shared` and `scopes`. Runtime-owned metadata is not accepted as user input.

Use stable roots for data that crosses node boundaries and a node-owned scope for local loop state. For example,
`shared.request.input` can be supplied by a Trigger while `scopes.answer.conversation` remains private to one model
loop.

## State Ports

A registered node or condition declares each port's schema, access mode, merge strategy, and optional capability contract.
The Graph Definition binds those ports by name.

Built-in ports can declare a `default_path`. Explicit bindings override defaults, and `{node_id}` placeholders are
resolved from the concrete node ID.

Port modes are `read`, `write`, and `read_write`. A required port must be bound (or have a valid default); optional ports
may be omitted. Capability ports bind a structured view, such as a conversation, and validate the relative fields a node
can access.

## Parallel writes

Parallel branches must write to disjoint paths unless the port contract defines a reducer with stable merge semantics. A typical
fan-out/fan-in workflow writes branch results separately and merges them in a downstream node.

Built-in reducers include `sum.v1`, `max.v1`, and `messages.v1`. Reducer IDs are versioned, so changing merge semantics is
an explicit contract change. Prefer disjoint branch paths when a reducer is not essential.

## Initial State

Initial State is business input, not runtime metadata. Analyze a graph's required initial fields before starting a Run and
provide only the paths needed by its entry path:

```json
{
  "shared": {
    "request": { "input": "Summarize this document" }
  }
}
```

The server's initial-state analysis endpoint and the Workbench identify required producers and bindings. Fix a missing
required field in the Trigger or Run input rather than weakening the node contract.

## Keep paths out of config

Do not place state paths in `config`. Configuration describes component behavior; `state` describes data access and is
validated against the registry contract.

## Binding checklist

1. Fetch the registry contract for the node or condition.
2. Bind every required port to a valid `shared` or `scopes` path.
3. Check that schemas and capability fields match the data at that path.
4. Keep parallel writes disjoint or name a reducer with stable merge semantics.
5. Rebuild the graph and create a new immutable Session after changing bindings.

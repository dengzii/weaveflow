# State Bindings

State Bindings connect a component's declared State Ports to concrete state paths. They make data access explicit and
allow invalid reads, writes, and parallel merge behavior to be rejected before execution.

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
- `scopes.<node>.*` isolates node or capability state.

External Run input may initialize `shared` and `scopes`. Runtime-owned metadata is not accepted as user input.

## State Ports

A registered node or condition declares each port's schema, access mode, merge strategy, and optional capability
contract. The Graph Definition binds those ports by name.

Built-in ports can declare a `default_path`. Explicit bindings override defaults, and `{node_id}` placeholders are
resolved from the concrete node ID.

## Parallel writes

Parallel branches must write to disjoint paths unless the port contract defines a deterministic reducer. A typical
fan-out/fan-in workflow writes branch results separately and merges them in a downstream node.

## Keep paths out of config

Do not place state paths in `config`. Configuration describes component behavior; `state` describes data access and is
validated against the registry contract.

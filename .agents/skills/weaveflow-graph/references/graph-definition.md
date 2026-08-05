# Graph Definition v2

Construct graphs from the live registry. Do not maintain a private list of node or condition schemas.

## Discovery Algorithm

1. Read `GET /registry` and unwrap `data`.
2. Select valid entries from `state_modules`; include at least one `{name, version}` reference.
3. For each node, find the matching `node_types[].type`.
4. Build `config` from that node type's `config_schema`. Respect required properties, types, enums, bounds, and
   `additionalProperties`.
5. Build `state` from `state_ports`:
    - Bind every required port that has no `default_path`.
    - Let the builder materialize a suitable `default_path` when one exists, or bind it explicitly when the workflow
      needs a different path.
    - Treat capability ports as a root binding for the capability's relative fields.
    - For `dynamic_state_ports`, validate the alias against `name_pattern`, `min_ports`, and `max_ports`.
6. For each conditional edge, find `conditions[].type` and apply its config and state schema the same way.
7. Validate the complete definition through `POST /graph/initial-state-requirements` before graph upload.

## Core Shape

```json
{
  "version": "2.0",
  "name": "graph-name",
  "description": "Optional description",
  "state_modules": [
    {
      "name": "weaveflow.protocols",
      "version": "1"
    }
  ],
  "entry_point": "first-node",
  "finish_point": "last-node",
  "nodes": [],
  "edges": [],
  "metadata": {}
}
```

Enforce these invariants:

- Use exactly version `2.0`.
- Include at least one state module and one node.
- Give every node a unique non-empty `id` and a registered `type`.
- Point `entry_point` and `finish_point` only at existing nodes when present.
- Use `__end__` only as an edge target.
- Do not repeat the same `from`/`to` edge pair.
- Use multiple ordinary outgoing edges for fan-out. Use registered conditions for branch selection.
- Keep runtime state paths in `state` bindings. Never put legacy input or output paths in `config`.
- Keep node behavior options such as `model_id`, prompts, tool IDs, and iteration limits in `config` only when allowed
  by that node's schema.

## Minimal Agent Example

Verify this example against the live registry before using it because registered modules, tools, and model settings can
differ:

```json
{
  "version": "2.0",
  "name": "single_agent",
  "description": "A single ReAct agent writes its final answer to shared state.",
  "state_modules": [
    {
      "name": "weaveflow.protocols",
      "version": "1"
    }
  ],
  "entry_point": "assistant",
  "finish_point": "assistant",
  "nodes": [
    {
      "id": "assistant",
      "name": "Assistant",
      "type": "agent",
      "config": {
        "model_id": "default",
        "tool_ids": [
          "calculator"
        ],
        "system_prompt": "Solve the task and return a concise final answer.",
        "max_iterations": 4
      },
      "state": {
        "task": {
          "path": "shared.request.input"
        },
        "conversation": {
          "path": "scopes.assistant.conversation"
        },
        "result": {
          "path": "shared.final.answer"
        }
      }
    }
  ],
  "edges": []
}
```

Provide the required task state when starting it:

```json
{
  "initial_state": {
    "shared": {
      "request": {
        "input": "Calculate 17 * 29 and explain the result."
      }
    }
  }
}
```

Before the run, confirm all of the following:

- `weaveflow.protocols@1` exists in `registry.data.state_modules`.
- `agent` exists in `registry.data.node_types` and its ports still match.
- `calculator` exists in `runtime/tools.data.tools`.
- `default` is enabled in `runtime/settings.data.models` and reports `api_key_configured: true`.
- Candidate analysis identifies `shared.request.input` as required, and the planned initial state supplies it.

## Identity And Versioning

The upload envelope carries `graph_id`, `graph_version`, the definition, and required graph-scoped `settings`; the
definition may also carry `metadata.id` and `metadata.graph_version`. Prefer explicit envelope values. Upload the
definition and settings together so node config, models, environment, and memory cannot refer to different revisions.

Record all returned identities:

- `graph_id`: stable logical identity used for triggers and historical queries.
- `graph_version`: caller-managed version label.
- `graph_hash`: executable semantic hash used by resume compatibility checks.
- `graph_snapshot_hash`: normalized full-definition hash, including metadata.
- `graph_session_id`: immutable uploaded session identity.

Changing canvas metadata can change the snapshot hash without changing the semantic hash. Changing node behavior or
resolved state bindings changes semantics and can prevent an old checkpoint from resuming on the current runner.
Changing only runtime settings still creates a new session even when the definition hashes are unchanged. Every
successful session is immediately available to both direct runs and triggers for its `graph_id`.

## Repository Examples And Truth Sources

Use these only after checking the live registry:

- `examples/supervisor_mode/graph.json` for supervisor routing and synthesis.
- `examples/state_operations/graph.json` for explicit state operations, dynamic state inputs, and CEL-backed conditions.
- `docs/graph-agent-workflow-patterns.md` for larger workflow patterns.
- `dsl/dsl.go`, `dsl/node_spec.go`, and `dsl/registry_schema.go` for Graph v2 serialization and schema generation.
- `node/agent.go` and other node definition files for implementation-specific behavior.

# Nodes and Tools

Nodes are the executable units in a Graph Definition. Each node has a stable ID, a registered type, optional configuration,
and State Port bindings. Tools are capabilities that model nodes or a dedicated tool-execution node can call.

## Built-in node groups

The default registry groups built-ins by purpose:

| Group | Examples |
| --- | --- |
| Input & Context | `user_input`, `conversation_message`, `context_reducer`, `environment_context` |
| Model & Tools | `llm_turn`, `text_generation`, `tool_execution` |
| Agents | `explore_agent` |
| Orchestration | `subgraph`, plan nodes, supervisor nodes |
| Output | `chat_reply` |
| State | `state_set`, `state_copy`, `state_delete`, `state_merge`, `state_append`, `state_transform` |

The Workbench gets the current list from `GET /registry`. Do not hard-code a node list in an editor or deployment.

## Configuration versus state

`config` changes component behavior, such as the model ID, tool IDs, prompt, expression, or parallel flag. `state` binds
the component's ports to data. For example:

```json
{
  "id": "answer",
  "type": "llm_turn",
  "config": {
    "model_id": "assistant",
    "tool_ids": ["calculator"],
    "reasoning_effort": "auto"
  },
  "state": {
    "conversation": { "path": "scopes.answer.conversation" },
    "output": { "path": "shared.answer" }
  }
}
```

Never put a state path in `config`. Paths in `state` can be inspected and validated against the registered contract.

## Model and tool loops

An `llm_turn` appends a model response to a bound conversation. If the response contains tool calls, a conditional edge
can route to `tool_execution`. That node runs the selected tools and appends their results to the same conversation. A
second conditional edge routes back to `llm_turn` until the conversation has a final answer.

Set `tool_ids` to the smallest allowlist needed by a node. Tool execution can be parallel for independent calls, but
parallel calls must still obey their state contracts and side-effect policy.

## State operation nodes

Use state nodes for model-free transformations:

- `state_set` replaces a bound path with a JSON value.
- `state_copy` copies one bound path to another.
- `state_merge` deep-merges JSON objects.
- `state_append` appends a value or array to a bound array.
- `state_delete` removes a bound path.
- `state_transform` evaluates restricted CEL over named inputs and writes a result.

For `state_transform`, bind dynamic input ports such as `price` and `quantity`, then use an expression such as
`inputs.price * inputs.quantity`. Keep expressions pure and repeatable; they are not a substitute for network or filesystem
tools.

## Conditions and failure routes

Conditions belong to edges. Built-in conditions include conversation tool-call/final-answer checks, state expressions,
plan status, and supervisor routing. Failure routes can match node or condition failures, selected error classes, or a
catch-all fallback. This keeps exceptional control flow visible in the graph.

## Choosing a node boundary

Create a separate node when a step has its own state contract, retry/effect policy, or inspection value. Keep related pure
transformations together when splitting them would add topology without improving observability. For a reusable component,
follow the [Custom Nodes](/guides/custom-nodes) guide.

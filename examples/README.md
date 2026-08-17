# Examples

The examples are grouped by the behavior they demonstrate. Start with the model-free runtime examples when learning
the execution model; they run entirely in memory and require no credentials or external services.

## Runtime control

| Command                                          | Demonstrates                                                                                                        |
|--------------------------------------------------|---------------------------------------------------------------------------------------------------------------------|
| `go run ./examples/graph/conditional_routing.go` | A structured `RouteDecision`, conditional selection, default fallback, and condition events.                        |
| `go run ./examples/graph/dynamic_map_reduce.go`  | Dynamic `Command.Send` fan-out, stable task ordering, parallel append and reducers, custom events, and artifacts.   |
| `go run ./examples/graph/failure_fallback.go`    | Classified execution errors, failure routes, `FailureContext`, failed-step persistence, and fallback return values. |
| `go run ./examples/graph/human_approval.go`      | `Command.Suspend`, checkpoint-backed resume input, dynamic `Goto`, and an explicit graph return value.              |
| `go run ./examples/graph/fan_in_fan_out.go`      | Static fan-out/fan-in, the parallel-wave barrier, and resume from an `after_wave` checkpoint.                       |

Each file has an `ignore` build tag so the scenarios can keep independent `main` functions inside the existing
`examples/graph` package. Run them by file as shown above.

## Agent workflows

These examples use an OpenAI-compatible model. Configure `OPENAI_API_KEY`, `OPENAI_BASE_URL`, and `OPENAI_MODEL` before
running them.

| Command                                                                         | Demonstrates                                                                           |
|---------------------------------------------------------------------------------|----------------------------------------------------------------------------------------|
| `go run ./examples/graph`                                                       | ReAct tool loop, local persistence, checkpoints, and continuation with new user input. |
| `go run ./examples/plan_mode "Calculate 125 * 48 and verify the result."`       | Planning, step execution, review, replanning, and final synthesis.                     |
| `go run ./examples/supervisor_mode "Compare two savings plans."`                | Supervisor routing, specialist delegation, and synthesis.                              |
| `go run ./examples/two_agent_handoff "Research explicit state bindings."`       | Agent-to-agent task handoff with isolated conversation roots.                          |
| `go run ./examples/multi_llm_turns "Draft and review an explanation."`          | Separate models and conversation roots with an explicit output-to-input handoff.       |
| `go run ./examples/shared_tool_loop "Use the calculator to evaluate 125 * 48."` | LLM, tool execution, and a condition sharing one conversation capability root.         |

## Definitions and focused nodes

| Path                                   | Demonstrates                                                                   |
|----------------------------------------|--------------------------------------------------------------------------------|
| `examples/state_operations/graph.json` | State set, copy, merge, append, transform, delete, and a CEL-backed condition. |
| `examples/supervisor_mode/graph.json`  | A complete editable Graph Definition v2 supervisor workflow.                   |
| `examples/dsl/`                        | Exporting the default registry and Graph Definition JSON schema.               |
| `examples/node/`                       | Focused construction and execution examples for individual node types.         |

Graph Definition v2 files keep state paths in component `state` bindings. Validate a definition against the current
registry before creating a Graph Session because registered node, condition, tool, and model settings may differ.

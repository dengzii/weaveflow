# Runnable Examples

The repository includes small, focused examples for topology, state contracts, runtime control, and multi-agent patterns.

## Agent workflows

These examples call a model. Configure an OpenAI-compatible provider with [Model Providers](/guides/model-providers)
before running them.

```bash
go run ./examples/graph
go run ./examples/plan_mode -profile multi-step
go run ./examples/supervisor_mode
```

## Workflows with explicit state bindings

```bash
go run ./examples/two_agent_handoff "Research explicit state bindings."
go run ./examples/multi_llm_turns "Draft and review an explanation."
go run ./examples/shared_tool_loop "Use the calculator to evaluate 125 * 48."
```

## Runtime control without a model

```bash
go run ./examples/graph/conditional_routing.go
go run ./examples/graph/dynamic_map_reduce.go
go run ./examples/graph/failure_fallback.go
go run ./examples/graph/human_approval.go
go run ./examples/graph/fan_in_fan_out.go
```

Start with the model-free examples when learning checkpoint, failure, approval, and parallel merge behavior. They run
quickly and do not require model credentials.

## Read the source alongside the run

| Goal | Start here |
| --- | --- |
| Conditional routing | `examples/graph/conditional_routing.go` |
| Dynamic fan-out and reducers | `examples/graph/dynamic_map_reduce.go` |
| Failure classification and fallback | `examples/graph/failure_fallback.go` |
| Human approval and resume | `examples/graph/human_approval.go` |
| Static fan-in/fan-out | `examples/graph/fan_in_fan_out.go` |
| Plan/execute/review | `examples/plan_mode/` |
| Supervisor/worker orchestration | `examples/supervisor_mode/` |
| Graph JSON and registry schema | `examples/dsl/` |

Most model-driven examples read provider settings from the process environment. Keep `.local/` output out of source
control; remove it when you need a fresh Session or checkpoint history.

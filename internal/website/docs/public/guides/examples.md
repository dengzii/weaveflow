# Runnable Examples

The repository includes focused examples for topology, state contracts, runtime control, and multi-agent patterns.

## Agent workflows

```bash
go run ./examples/graph
go run ./examples/plan_mode "Calculate 125 * 48 and verify the result."
go run ./examples/supervisor_mode "Compare two saving plans."
```

## Explicit state bindings

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

Start with model-free examples when learning checkpoint, failure, approval, and parallel merge behavior. They are faster
to run and do not require model credentials.

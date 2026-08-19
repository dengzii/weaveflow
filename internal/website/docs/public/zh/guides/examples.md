# 可运行示例

仓库提供了用于理解拓扑、状态契约、运行时控制和多智能体模式的专注示例。

## 智能体工作流

```bash
go run ./examples/graph
go run ./examples/plan_mode "Calculate 125 * 48 and verify the result."
go run ./examples/supervisor_mode "Compare two saving plans."
```

## 显式状态绑定

```bash
go run ./examples/two_agent_handoff "Research explicit state bindings."
go run ./examples/multi_llm_turns "Draft and review an explanation."
go run ./examples/shared_tool_loop "Use the calculator to evaluate 125 * 48."
```

## 无需模型的运行时控制

```bash
go run ./examples/graph/conditional_routing.go
go run ./examples/graph/dynamic_map_reduce.go
go run ./examples/graph/failure_fallback.go
go run ./examples/graph/human_approval.go
go run ./examples/graph/fan_in_fan_out.go
```

学习检查点、失败处理、审批和并行合并行为时，建议从无需模型的示例开始。它们运行更快，也不需要模型凭据。

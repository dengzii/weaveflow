# 可运行示例

仓库提供了一组聚焦的示例，分别演示拓扑、状态契约、运行时控制和多智能体模式。

## 智能体工作流

这些示例需要调用模型。运行前，请按照[模型提供商](/zh/guides/model-providers)配置一个 OpenAI 兼容服务。

```bash
go run ./examples/graph
go run ./examples/plan_mode -profile multi-step
go run ./examples/supervisor_mode
```

## 带显式状态绑定的工作流

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

学习检查点、失败处理、审批和并行合并时，建议从无需模型的示例开始。它们运行更快，也不需要模型凭据。

## 按目标选择示例

| 目标 | 示例 |
| --- | --- |
| 条件路由 | `examples/graph/conditional_routing.go` |
| 动态扇出和归并（reducer） | `examples/graph/dynamic_map_reduce.go` |
| 错误分类与回退 | `examples/graph/failure_fallback.go` |
| 人工审批与恢复 | `examples/graph/human_approval.go` |
| 静态扇入/扇出 | `examples/graph/fan_in_fan_out.go` |
| Plan/Execute/Review | `examples/plan_mode/` |
| Supervisor/Worker | `examples/supervisor_mode/` |
| Graph JSON 与注册表 schema | `examples/dsl/` |

大多数模型示例从进程环境读取提供商设置。不要提交 `.local/` 输出；需要新的 Session 或检查点历史时，可以删除这些文件。

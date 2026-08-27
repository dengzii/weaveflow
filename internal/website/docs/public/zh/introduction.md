# 为什么选择 WeaveFlow？

WeaveFlow 是一个用 Go 构建的工作流运行时，适合需要在流程运行后继续解释、检查和恢复的场景。
每个工作流都由一张带版本的图描述：节点负责执行工作，边描述控制流，State Ports（状态端口）明确每个组件可以读写哪些数据。
本文保留 Graph、Run、State、Checkpoint 等 API 术语，方便对照代码和接口。

## 它解决什么问题

智能体循环很适合快速做原型，但系统变复杂后，你通常还需要回答：

- 哪个模型、工具和提示词生成了这个答案？
- 为什么走了这条分支，而不是另一条？
- 失败或等待审批前，状态发生了什么变化？
- 进程重启后能否从检查点继续，且不会重复副作用？

WeaveFlow 将这些决策写进图定义，并保存运行后用于检查的证据。

## 设计原则

### 显式控制流

用节点、普通边、条件边和失败路由表达顺序、路由、审批和回退。图可以序列化为 JSON，在 Pull Request（PR）中审查，
也可以在 Workbench 中编辑。

### 契约约束的状态

每个注册节点和条件都会暴露 State Ports。定义将端口绑定到 `shared.request.input` 或
`scopes.planner.plan` 等路径，注册表会在运行前验证访问模式、schema、能力和并行合并规则。

### 持久化证据

运行时保存 Runs、Steps、Events、Checkpoints 和 Artifacts，因此可以检查从输入到输出的全过程，包括人工输入和
副作用结果确认。

### 本地优先

Go 运行时、调试服务器和 Workbench 都能在本机运行。Graph Definition 保持可移植，凭据和运行数据留在定义之外。
你可以先使用内存 Runner，再切换到本地持久化 Runner，或使用 Docker Compose 部署镜像。

## 适用场景

- 带工具调用的 ReAct 工作流。
- Plan/Execute/Review 或 Supervisor/Worker 编排。
- 人工审批、暂停/恢复和检查点恢复。
- 可复现的状态转换与扇出/扇入流水线。
- 需要可视化调试和审计执行历史的团队。

WeaveFlow 不是托管的多租户控制平面；认证、配额、租户隔离和跨 Worker 接管由部署环境负责。

```text
输入 → 规划 → 并行 Worker → 审查 → 需要审批？ → 答案
             ↘ 失败路由 ↗
```

请从[快速开始](/zh/getting-started)开始，然后阅读[图定义](/zh/concepts/graph-definition)和
[运行时模型](/zh/concepts/runtime)。

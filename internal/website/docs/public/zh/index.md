---
layout: home

hero:
  name: WeaveFlow
  text: Go 语言确定性智能体工作流
  tagline: 构建显式图、约束状态访问、检查每次运行，并从检查点恢复执行。
  actions:
    - theme: brand
      text: 快速开始
      link: /zh/getting-started
    - theme: alt
      text: 打开体验平台
      link: https://playground.weaveflow.space

features:
  - title: 显式图拓扑
    details: 使用可序列化的节点、边、条件和入口点定义控制流，而不是把它隐藏在智能体循环中。
  - title: 契约约束的状态
    details: 将 State Ports 绑定到显式路径，并在执行前验证读写、能力和并行合并行为。
  - title: 可检查的运行时
    details: 持久化 Runs、Events、Checkpoints 和 Artifacts，让执行过程可以检查、暂停、恢复和续跑。
---

## WeaveFlow 是什么？

WeaveFlow 是一个使用 Go 构建、执行和检查 LLM 智能体的图运行时。它结合了声明式 Graph Definition、
确定性执行、检查点状态，以及用于模型调用、工具使用、规划、路由和人工审批的可复用节点。

项目采用本地优先方式：运行时和 Workbench 可以在你的机器上运行，而 Graph Definition 始终保持为可移植的
JSON 文档。

## 选择一个入口

- 阅读[快速开始](/zh/getting-started)，安装模块并运行示例。
- 了解[图定义](/zh/concepts/graph-definition)如何描述拓扑。
- 在构建自定义节点前理解[状态绑定](/zh/concepts/state-bindings)。
- 使用 [Workbench](/zh/guides/workbench) 编辑图并检查运行记录。

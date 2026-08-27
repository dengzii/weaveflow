---
layout: home

hero:
  name: WeaveFlow
  text: 用 Go 构建可检查的智能体工作流
  tagline: 构建显式图、限制状态访问、检查每次运行，并从检查点恢复
  actions:
    - theme: brand
      text: 快速开始
      link: /zh/getting-started
    - theme: alt
      text: 打开体验平台
      link: https://playground.weaveflow.space

features:
  - title: 显式图拓扑
    details: 使用可序列化的节点、边、条件和入口点定义控制流，而不是把它隐藏在智能体循环中
  - title: 契约约束的状态
    details: 将 State Ports（状态端口）绑定到显式路径，并在执行前验证读写、能力和并行合并行为
  - title: 可检查的运行时
    details: 持久化每次运行的 Run、Event、Checkpoint 和 Artifact 记录，支持查看、暂停和恢复运行
---

## WeaveFlow 是什么？

WeaveFlow 是一个用 Go 构建的图运行时，用于搭建、执行和检查 LLM 智能体工作流。它将声明式 Graph Definition（图定义）、
清晰的执行路径、检查点状态，以及用于模型调用、工具使用、规划、路由和人工审批的可复用节点结合起来。

项目以本地运行为主：运行时和 Workbench 可以直接在你的机器上运行，Graph Definition 也始终是可移植的 JSON 文档。

## 选择适合你的入口

- 刚开始接触 WeaveFlow？先读[为什么选择 WeaveFlow？](/zh/introduction)，再开始[快速开始](/zh/getting-started)。
- 设计工作流？先了解[图定义](/zh/concepts/graph-definition)、[状态绑定](/zh/concepts/state-bindings)和
  [节点与工具](/zh/concepts/nodes-and-tools)。
- 要接入模型或业务组件？阅读[模型提供商](/zh/guides/model-providers)和[自定义节点](/zh/guides/custom-nodes)。
- 要查看运行情况？使用 [Workbench](/zh/guides/workbench)、[可观测性](/zh/guides/observability) 和 [HTTP API](/zh/reference/http-api)。
- 准备部署？按照[部署服务器](/zh/deployment)操作，再查看[配置参考](/zh/reference/configuration)和[故障排查](/zh/troubleshooting)。

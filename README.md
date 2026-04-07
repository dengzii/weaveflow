# WeaveFlow

`WeaveFlow` 是一个面向本地 LLM / Agent Graph 的运行与调试工具集，使用 Go 实现。

项目重点不是做一个完整的在线 Agent 平台，而是提供一套便于本地实验、图编排、运行时调试、断点恢复和持久化的基础设施。项目已经具备可运行的
Graph DSL、内置节点、运行时，但整体仍处于持续演进阶段。

## 项目目标

- 提供可序列化的 Graph DSL，用于描述节点、边、条件和运行配置
- 提供本地优先的 Agent Graph 运行时，支持 step、checkpoint、artifact 和 event
- 支持图执行过程中的暂停、恢复、调试和状态快照
- 为本地模型、工具调用和人工介入流程提供统一编排方式
- 为后续更完整的服务化接口保留稳定内核

## 示例

可以从 `examples/graph` (ReAct 风格图执行示例) 快速了解当前能力：

```shell
export OPENAI_API_KEY=your_openai_api_key
export OPENAI_BASE_URL=open_ai_api_base_url
export OPENAI_MODEL=model_name

go run examples/graph
```

## 项目现状

当前项目更适合：

- 本地 Agent Graph 调试
- 运行时能力验证
- 节点 / 条件 / 工具机制扩展
- 断点恢复和状态持久化实验

当前还不适合直接视为：

- 完整的生产级 Agent 平台
- 稳定完备的 HTTP 工作流服务
- 已充分抽象好的多模型统一网关

## TODO

- 完善 `internal/server/` 的 graph API，补齐更稳定的运行、查询和恢复接口
- 统一 DSL、Registry、Runtime、HTTP 对各字段的消费链路，减少“结构已定义但未接入”的情况
- 补全默认节点、条件、工具的 schema、示例和文档
- 明确 `llama_cpp` 相关能力的默认接入策略与环境依赖说明
- 持续完善 runtime 的测试覆盖，尤其是状态合并、恢复和持久化链路
- 增强调试输出与脱敏策略，减少 prompt、tool 参数和本地路径泄露风险
- 逐步梳理 memory 模块与默认 graph 执行链路之间的集成方式

## 适合谁使用

如果你正在做下面这些事情，这个仓库会比较合适：

- 想在本地搭一个可调试的 Agent Graph 原型
- 想验证节点式编排、工具调用和人工介入流程
- 想研究 Go 里的图运行时、状态快照和恢复机制
- 想在现有内核基础上继续补服务层或产品化能力

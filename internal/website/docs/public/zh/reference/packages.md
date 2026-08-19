# 包结构

| 包 | 职责 |
| --- | --- |
| `core/` | 共享执行契约、模型与工具抽象，以及通用运行时类型。 |
| `dsl/` | 可序列化 Graph Definitions、组件 schema 和 State Port 契约。 |
| `graph/` | 拓扑验证、边解析、图编译和轻量执行。 |
| `runtime/` | Run 生命周期、检查点、事件、制品、恢复和所有权策略。 |
| `state/` | 状态路径、快照、补丁、投影、验证和合并行为。 |
| `capability/` | 会话、计划、监督和执行协议的类型化路径绑定视图。 |
| `registry/` | 节点、条件、State Module、能力元数据和构建装配。 |
| `node/` | 面向生产使用的内置节点。 |
| `builtin/` | 内置条件、辅助函数和默认注册表装配。 |
| `tools/` | 随项目提供的工具实现。 |
| `llms/` | 模型集成，包括兼容 OpenAI 的端点。 |
| `cmd/server/` | 调试服务器入口。 |
| `internal/server/` | 调试服务器 API 实现。 |
| `internal/web/` | Workbench 图编辑器和 Run 调试器。 |

查看导出的 Go 标识符和函数签名时，请将这些概念指南与 [pkg.go.dev](https://pkg.go.dev/github.com/dengzii/weaveflow) 配合使用。

# 状态绑定

State Bindings（状态绑定）将组件声明的 State Ports（状态端口）连接到具体状态路径，明确每个组件可以访问哪些数据，并在执行前
拦截无效读写，避免不可预期的并行合并结果。

```json
{
  "id": "llm",
  "type": "llm_turn",
  "config": {
    "tool_ids": ["calculator"]
  },
  "state": {
    "conversation": {
      "path": "scopes.llm.conversation"
    },
    "output": {
      "path": "shared.final.answer"
    }
  }
}
```

## 状态根路径

- `shared.*` 存放有意在节点之间共享的图级数据。
- `scopes.<node>.*` 隔离节点或能力的本地状态。

外部 Run 输入可以初始化 `shared` 和 `scopes`，但不能写入运行时元数据。

跨节点数据使用稳定根路径，节点内部循环状态使用节点专属 scope。例如，Trigger 可以提供 `shared.request.input`，
而 `scopes.answer.conversation` 只属于一个模型循环。

## State Ports

已注册的节点或条件会声明每个端口的 schema、访问模式、合并策略和可选能力契约。Graph Definition 按名称绑定这些端口。

内置端口可以声明 `default_path`。显式绑定会覆盖默认值，`{node_id}` 占位符会根据具体节点 ID 解析。

端口模式为 `read`、`write` 和 `read_write`。必需端口必须绑定或拥有有效默认值；可选端口可以省略。Capability 端口可以
绑定会话等结构化视图，并验证节点能够访问哪些相对字段。

## 并行写入

并行分支必须写入不同路径，除非端口契约指定了能够稳定合并结果的 reducer。典型的扇出/扇入工作流分别写入各分支结果，
再由下游节点合并。

内置 reducer 包括 `sum.v1`、`max.v1` 和 `messages.v1`。Reducer ID 带版本，因此合并语义变化会成为明确的契约变更。
如果不需要 reducer，优先让并行分支写入互不重叠的路径。

## 不要把路径放进 config

不要在 `config` 中配置状态路径。`config` 描述组件行为，`state` 描述数据访问，并由注册表契约验证。

## 初始 State

初始 State 是业务输入，不是运行时元数据。启动 Run 前先分析必需字段，只提供入口路径所需的数据：

```json
{
  "shared": { "request": { "input": "请总结这份文档" } }
}
```

服务器的初始状态分析接口和 Workbench 会指出必需生产者与绑定。缺少必需字段时，应修正 Trigger 或 Run 输入，而不是
削弱节点契约。

## 绑定检查清单

1. 获取节点或条件的注册表契约。
2. 将每个必需端口绑定到有效的 `shared` 或 `scopes` 路径。
3. 确认 schema 和能力字段与该路径中的数据匹配。
4. 让并行写入互不重叠，或明确指定能够稳定合并结果的 reducer。
5. 修改绑定后重新构建图，并创建新的不可变 Session。

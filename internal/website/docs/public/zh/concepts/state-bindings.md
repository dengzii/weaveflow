# 状态绑定

State Bindings 将组件声明的 State Ports 连接到具体状态路径。它让数据访问保持显式，并在执行前拒绝无效读写和
不确定的并行合并行为。

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
- `scopes.<node>.*` 隔离节点或能力的状态。

外部 Run 输入可以初始化 `shared` 和 `scopes`。运行时拥有的元数据不接受用户输入。

## State Ports

已注册的节点或条件会声明每个端口的 schema、访问模式、合并策略和可选能力契约。Graph Definition 按名称绑定这些端口。

内置端口可以声明 `default_path`。显式绑定会覆盖默认值，`{node_id}` 占位符会根据具体节点 ID 解析。

## 并行写入

并行分支必须写入不同路径，除非端口契约定义了确定性 reducer。典型的扇出/扇入工作流分别写入各分支结果，
再由下游节点合并。

## 不要把路径放进 config

不要在 `config` 中配置状态路径。`config` 描述组件行为，`state` 描述数据访问，并由注册表契约验证。

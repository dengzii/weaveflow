# 节点与工具

节点是 Graph Definition（图定义）中可执行的单元。每个节点都有稳定 ID、注册类型、可选配置和 State Port（状态端口）绑定。
工具是模型节点或专门的工具执行节点可以调用的外部能力。

## 内置节点分组

默认注册表按用途组织内置节点：

| 分组 | 示例 |
| --- | --- |
| 输入与上下文 | `user_input`、`conversation_message`、`context_reducer`、`environment_context` |
| 模型与工具 | `llm_turn`、`text_generation`、`tool_execution` |
| 智能体 | `explore_agent` |
| 编排 | `subgraph`、Plan 节点、Supervisor 节点 |
| 输出 | `chat_reply` |
| 状态 | `state_set`、`state_copy`、`state_delete`、`state_merge`、`state_append`、`state_transform` |

Workbench 通过 `GET /registry` 获取当前列表。不要在编辑器或部署脚本中硬编码节点清单。

## 配置与状态

`config` 改变组件行为，例如模型 ID、工具 ID、提示词、表达式或并行开关；`state` 则把端口绑定到数据：

```json
{
  "id": "answer",
  "type": "llm_turn",
  "config": { "model_id": "assistant", "tool_ids": ["calculator"] },
  "state": {
    "conversation": { "path": "scopes.answer.conversation" },
    "output": { "path": "shared.answer" }
  }
}
```

不要把状态路径放入 `config`。注册表只会根据契约检查 `state` 中的路径。

## 模型与工具循环

`llm_turn` 会把模型响应追加到绑定的会话。如果响应包含工具调用，条件边可以将流程路由到 `tool_execution`；工具结果
会追加回同一会话，再由另一条条件边路由回模型，直到得到最终答案。只给每个节点配置实际需要的 `tool_ids`。

## 状态操作节点

状态操作节点不调用模型，适合执行可重复的数据变换：

- `state_set` 用 JSON 值替换绑定路径。
- `state_copy` 在两个绑定路径之间复制值。
- `state_merge` 深度合并 JSON 对象。
- `state_append` 向绑定数组追加值或数组。
- `state_delete` 删除绑定路径。
- `state_transform` 使用受限 CEL 计算绑定输入并写入结果。

`state_transform` 可以绑定 `price`、`quantity` 等动态输入，并使用 `inputs.price * inputs.quantity` 这样的表达式。
表达式应保持纯函数性质并可重复计算，不要用它访问网络或文件系统。

## 条件和失败路由

条件属于边。内置条件用于判断工具调用、最终答案、状态表达式、Plan 状态和 Supervisor 路由。失败路由可以根据节点或条件
阶段、错误分类，或 catch-all 规则选择备用目标，让异常控制流保持可见。

## 选择节点边界

当一个步骤需要独立的状态契约、重试/副作用策略或单独检查时，应将它拆成单独节点。新建可复用组件请参见[自定义节点](/zh/guides/custom-nodes)。

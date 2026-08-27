# 图定义

Graph Definition（图定义）是描述工作流拓扑和组件配置的可序列化权威文档。它声明图版本、State Modules（状态模块）、节点、
边和入口点。为便于对照代码和接口，本文保留 Graph、Run、State 等 API 术语的英文写法。

```json
{
  "version": "1.0",
  "name": "assistant",
  "state_modules": [
    { "name": "weaveflow.protocols", "version": "1" }
  ],
  "entry_point": "input",
  "nodes": [
    {
      "id": "input",
      "type": "user_input",
      "state": {
        "value": { "path": "shared.request.input" }
      }
    }
  ],
  "edges": []
}
```

## 顶层字段

| 字段 | 含义 |
| --- | --- |
| `version` | 定义 schema 的版本；当前值为 `1.0`。 |
| `name`、`description` | Workbench 中显示的可读名称和说明。 |
| `state_modules` | 提供状态字段和能力的版本化模块。 |
| `entry_point` | 初始执行从此节点开始。 |
| `finish_point` | 可选的明确结束节点。 |
| `nodes` | 已注册的可执行组件。 |
| `edges` | 普通、条件和失败路由。 |
| `policy` | 图级执行限制和行为配置。 |
| `metadata` | 由应用自行定义的 JSON 元数据。 |

验证要求至少提供 `state_modules` 和 `nodes`。节点 ID 必须唯一；每条边的起点必须是节点，终点必须是节点或保留引用 `__end__`。

## 节点

每个节点都有稳定 ID 和已注册类型。组件设置放在 `config` 中，State Port（状态端口）绑定放在 `state` 中。分离这两部分，
注册表就能在运行前验证状态访问。

## 边与条件

普通边连接两个节点。条件属于边，用来判断路径是否可用；条件不是单独的节点。

无条件的顺序执行使用普通边；路由、工具调用循环、审批决策和失败回退使用条件边。

一条边只能包含一个条件或一个失败路由。条件解析器返回 true 时，条件边才可用；失败路由则在匹配的节点或条件出错后
选择备用目标。让回退边与正常成功路径分开，图的意图会更清楚。

## 执行策略

可选的 `policy` 对象用于设置图级限制，例如执行时长、状态大小、并发数以及重试或检查点行为。策略会参与定义哈希，
并随 Graph Session 一起保存。开发阶段建议使用保守限制，结合真实 Run 的证据再逐步调高。

## 不可变 Session

调试服务器保存 Graph，并为执行创建不可变的 Graph Session。运行时设置会随 Session 一起保存，因此可以把 Run 追溯到
生成它的确切定义和配置。

## 验证

执行前，注册表和图构建器会验证组件类型、State Modules、State Port 绑定、入口点、边目标、必需的数据生产者以及
并行写入冲突。

写入文件前，先在代码中验证完整定义：

```go
if err := definition.Validate(); err != nil {
    log.Fatal(err)
}
graph, err := weaveflow.BuildGraph(builtin.NewDefaultRegistry(), definition)
if err != nil {
    log.Fatal(err)
}
```

仅检查 JSON 结构还不够。使用注册表构建图时，还能发现未知节点类型、能力契约不匹配、不支持的 reducer 或缺少必需绑定等问题。

## 版本管理建议

把定义和注册表视为一组：将 JSON 纳入版本控制，并在同一个变更中审查拓扑和状态修改。行为发生变化后创建新的 Graph
Session；已有 Session 保持不可变，这样历史 Run 才能对应到创建它们时的定义和设置。

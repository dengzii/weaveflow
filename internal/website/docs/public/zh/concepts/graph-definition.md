# 图定义

Graph Definition 是拓扑和组件配置可序列化的事实来源。它声明图版本、State Modules、节点、边和入口点。

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

## 节点

每个节点都有稳定的 ID 和已注册类型。组件设置放在 `config` 中，State Port 绑定放在 `state` 中。保持两者分离，
可以让注册表在运行开始前验证状态访问。

## 边与条件

普通边连接两个节点。条件属于边，用于判断该路径是否可用；条件不会表现为独立节点。

无条件顺序执行使用普通边；路由、工具调用循环、审批决策和失败回退路径使用条件边。

## 不可变 Session

调试服务器保存 Graph，并为执行创建不可变的 Graph Session。运行时设置会随 Session 一起保存，因此可以将 Run
追溯到生成它的确切定义和配置。

## 验证

执行前，注册表和图构建器会验证组件类型、State Modules、State Port 绑定、入口点、边目标、必需的数据生产者，
以及并行写入冲突。

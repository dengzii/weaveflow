# Node State 解耦与可组合化重构方案

## 背景

当前项目已经具备图编排、节点注册、状态契约、运行时恢复等基础能力，但 `node state` 已经在事实上形成了较强依赖，导致节点难以自由组合、复用和替换。

这个问题不是单个业务节点实现不够干净，而是执行接口和状态模型本身就在鼓励耦合。

## 当前问题定位

### 1. 节点直接接收完整 State

当前节点接口定义为：

```go
type Node[S any] interface {
	ID() string
	Name() string
	Description() string
	Invoke(ctx context.Context, state S) (S, error)
}
```

这意味着每个节点天然都可以：

- 读取整棵状态树
- 修改任意状态路径
- 形成对其他节点输出结构的隐式依赖

直接影响是：

- 节点组合依赖“前面是谁”，而不是“我需要什么输入”
- 节点复用时必须了解历史状态结构
- 节点一旦增多，图会逐渐变成隐式共享全局状态

### 2. StateContract 目前主要用于事后校验

项目里已经存在 `StateContract` 和 `NodeWriteContract` 机制，这一点非常重要，说明系统已经有了往契约化方向收敛的基础。

但当前 contract 的主要作用仍然是：

- 从 registry 解析节点的读写声明
- 在 runner 中对节点写入结果做校验
- 对越权写入进行 warn/strict 检查

也就是说，contract 现在更像“审计工具”，而不是“执行边界”。

节点在执行时，仍然直接看到完整状态树。

### 3. 状态树本身过于开放

当前 `runtime.State` 是一个通用的 `map[string]any`，支持：

- 任意路径解析
- 任意层级 map 合并
- root、scope、namespace 混合写入

这本身不是错误，但在没有执行边界时，会直接导致：

- 节点可以顺手访问不属于自己的数据
- 节点私有状态和共享状态容易混在一起
- graph 组合成功依赖人工经验，而不是 contract 分析

### 4. Subgraph 仍然是整状态透传

当前 subgraph 的状态契约使用 `*` wildcard，即：

- 输入是完整状态
- 输出也是完整状态回写

这会让子图直接突破上层 contract 边界，放大耦合。

### 5. 节点依赖的是前驱结构，不是输入能力

现在很多节点虽然表面上声明了 state path，但执行模型仍然允许它们依赖：

- 某个上游节点的具体输出结构
- 某个特定 graph 的执行顺序
- 某个 namespace 是否恰好已经被前一个节点填充

这会让“节点可组合”退化成“节点只能在特定图里工作”。

## 设计目标

这次重构的目标不是把状态系统做复杂，而是把节点从“直接操作全局状态”改成“基于契约读写受控数据”。

目标分为四层：

### 1. Graph Wiring

图层只负责：

- 节点有哪些
- 节点怎么连接
- 条件边如何选择
- 子图何时调用

图层不再承担隐式状态依赖管理。

### 2. Node Contract

每个节点显式声明：

- 读哪些路径
- 写哪些路径
- 哪些输出是 required
- 某些字段如何 merge

节点依赖的是数据契约，不是前驱节点实例。

### 3. Node Runtime View

runner 在执行节点前，根据 contract 从全局状态中裁出最小输入视图。

节点执行时只能看到它应该看到的部分。

节点执行后不再返回完整状态，而是返回一个输出 patch。

### 4. Global State Store

保留现有 `runtime.State` 作为最终持久化状态树，用于：

- checkpoint
- resume
- artifact 绑定
- graph 全局持久化

但节点不再直接控制完整状态树。

## 目标执行模型

当前模型：

```go
Invoke(ctx, fullState) -> fullState
```

目标模型：

```go
Execute(ctx, inputView) -> outputPatch
```

其中：

- `inputView` 只包含 contract 允许读取的路径
- `outputPatch` 只包含 contract 允许写入的路径
- patch merge 由 runner 统一完成

## 分阶段落地方案

## 第一阶段：把 contract 从校验机制升级成执行边界

这是收益最大、改动风险最低的一步。

### 改造目标

在不大面积改动业务节点的前提下，让 runner 先按 contract 执行。

### 具体做法

在 `runtime` 下新增两类能力：

1. 状态投影

```go
func ProjectStateByContract(full State, contract dsl.StateContract) State
```

作用：

- 根据节点声明的读路径
- 从完整状态树中裁出最小输入视图
- 未声明的状态不暴露给节点

2. patch 合并

```go
func MergePatchByContract(full State, patch State, contract dsl.StateContract) (State, error)
```

作用：

- 节点执行只返回 patch
- runner 只把允许写的路径合回全局状态
- 越权写入在 merge 时直接拦截

### runner 改造方式

当前 runner 逻辑可以概括为：

1. 直接把完整 state 传给 node
2. node 返回下一份完整 state
3. runner 对 diff 结果做 contract 校验

改造后变成：

1. runner 根据 contract 生成 input view
2. node 基于 input view 执行
3. node 返回 patch
4. runner 根据 contract merge patch
5. 保留 validate 作为保护网

### 兼容旧节点

不要第一步就强制改所有节点。

建议新增一个兼容适配器：

```go
type LegacyNodeAdapter struct {
	Inner nodes.Node[runtime.State]
}

func (a *LegacyNodeAdapter) Execute(ctx context.Context, input runtime.State) (runtime.State, error) {
	next, err := a.Inner.Invoke(ctx, input.CloneState())
	if err != nil {
		return nil, err
	}
	return DiffState(input, next), nil
}
```

这样可以做到：

- 老节点继续复用
- 新 runner 已经切换到 patch 执行语义
- 后续节点可以逐个迁移

这是整个方案里最关键的过渡层。

## 第二阶段：正式区分“完整状态”和“节点视图”

第一阶段完成后，接口语义已经变化，但类型可能还是 `State`。

第二阶段建议正式引入节点执行接口：

```go
type ContractNode interface {
	ID() string
	Name() string
	Description() string
	Execute(ctx context.Context, input runtime.State) (runtime.State, error)
}
```

这里仍然保留 `runtime.State`，但语义明确为：

- 输入不是 full state，而是 input view
- 返回值不是 next full state，而是 output patch

这样可以在不引入大量新类型的前提下，先完成运行时语义分离。

后续如果需要更强类型化，再进一步演进。

## 第三阶段：把状态分区固定下来

当前状态虽然已经有 scope 和 namespace 概念，但边界仍然不够硬。

建议统一成以下四个分区：

### 1. `shared.*`

跨节点共享的业务状态。

适合放：

- intent
- planner
- orchestration
- memory
- execution result

### 2. `scopes.<scope>.*`

作用域状态。

适合放：

- 对话消息
- 工具循环
- 某个子任务上下文

### 3. `runtime.<node_id>.*`

节点私有执行态。

适合放：

- iterator 当前索引
- 审批节点临时等待态
- 某一步执行中间标记

这类状态不应该被普通业务节点消费。

### 4. `internal.*`

平台保留状态。

用于：

- 运行时控制信息
- artifact 索引
- 系统级基础设施字段

普通节点不应通过 wildcard 访问它。

### 这样做的意义

- 共享数据和私有执行态分离
- contract path 的语义更稳定
- graph 依赖分析能建立在固定分区之上

## 第四阶段：把“依赖前驱节点”改成“依赖数据能力”

这是实现灵活 node 组合的核心。

### 错误依赖方式

- Verifier 必须在 Planner 后面
- ContextAssembler 必须在 MemoryRecall 后面
- Finalizer 只能处理某个固定 graph 的输出

### 正确依赖方式

- Verifier 依赖 `shared.planner`
- ContextAssembler 依赖 `shared.memory` 和 `shared.orchestration`
- Finalizer 依赖 `scopes.<scope>.messages` 和某个共享结果字段

这样：

- 谁生产这些字段都行
- graph 可以替换中间节点
- 同一节点可以跨图复用

## 第五阶段：在建图阶段做 contract-level 依赖分析

仅有 contract 还不够，graph 组合时还需要分析 contract 是否满足。

建议在 `Registry.BuildGraph` 或 `Graph.Validate` 阶段增加静态检查。

### 分析内容

对每个节点：

- 列出 read paths
- 列出 write paths
- 判断其 read paths 是否能被满足

满足来源可以是：

- graph 初始输入
- 上游节点输出
- 平台保留字段

### 应报错的情况

- 节点 required read path 没有来源
- 多个节点写同一路径但 merge 语义冲突
- 节点只在 wildcard subgraph 下才能成立

### 应告警的情况

- 同一路径来源不唯一
- 节点 contract 过宽
- 某些字段存在覆盖式写入风险

这样，组合问题就能在 build/validate 阶段暴露，而不是等运行时才踩坑。

## 第六阶段：治理 Subgraph wildcard

`subgraph` 是当前 contract 体系里最大的破口。

建议拆成两种模式：

### 1. passthrough_subgraph

保留现状：

- 输入完整状态
- 输出完整状态

仅用于兼容老图，不建议新图继续使用。

### 2. mapped_subgraph

通过显式输入输出映射调用子图，例如：

```json
{
  "graph_ref": "foo/bar",
  "input_map": {
    "shared.intent": "shared.intent",
    "shared.plan": "shared.plan"
  },
  "output_map": {
    "shared.result": "shared.result",
    "shared.observations": "shared.observations"
  }
}
```

这样子图才能成为真正可复用的组合单元，而不是“隐藏着整状态耦合的黑盒”。

## 第七阶段：按收益排序迁移节点

不要全量同时迁移，建议按以下顺序推进。

### 第一批

- IteratorNode
- MemoryRecallNode
- MemoryWriteNode

原因：

- 状态边界清晰
- patch 语义简单
- 容易成为迁移样板

### 第二批

- PlannerNode
- VerifierNode
- ObservationRecorderNode

原因：

- 这些节点最能体现“依赖数据能力，不依赖前驱节点”的收益

### 第三批

- LLMNode
- ToolsNode
- ContextAssemblerNode

原因：

- 涉及 conversation scope
- 复杂度较高
- 但一旦迁移完成，对组合性提升最明显

### 最后一批

- SubgraphNode

原因：

- 影响面最大
- 需要输入输出映射模型一起落地

## 建议的代码落点

## 新增文件

建议在 `runtime/` 新增：

### `state_project.go`

负责：

- 基于 contract 生成节点输入视图
- 过滤未声明读取路径

### `state_patch.go`

负责：

- 定义 patch 语义
- 生成 diff
- 受控 merge patch

### `node_execution.go`

负责：

- 统一封装 legacy node 和新 contract node
- 隔离 runner 和节点接口变更

## 重点修改文件

### `runtime/graph_runner.go`

从：

- full state 直传节点

改成：

- project -> execute -> merge

### `graph_runner_graph.go`

增强：

- 读写 contract 解析
- 合法路径转换
- graph 级别 contract 分析支持

### `registry.go`

增强：

- 节点 contract 解析能力
- 后续可扩展 read/write contract 统一入口

### `runtime/contract_validation.go`

从只校验写入越权，扩展为：

- 越权读取
- 越权写入
- required path 缺失

## 暂时不建议重写的部分

以下能力已经足够稳定，优先复用：

- `runtime/state.go`
- `runtime/state_path.go`
- `runtime/state_merge.go`

当前问题主要不在底层状态工具，而在节点执行边界。

## 迁移后的节点示例

以 iterator 为例。

### 输入视图

```json
{
  "shared": {
    "items": [
      {"id": 1},
      {"id": 2},
      {"id": 3}
    ]
  },
  "runtime": {
    "iterator_1": {
      "next_index": 1
    }
  }
}
```

### 输出 patch

```json
{
  "runtime": {
    "iterator_1": {
      "index": 1,
      "iteration": 2,
      "item": {"id": 2},
      "next_index": 2,
      "done": false
    }
  }
}
```

而不是返回整棵状态树。

这会带来两个直接收益：

- 节点不会顺手读取不相关状态形成隐式依赖
- 节点组合只需要满足 contract，不需要固定前驱

## 实施顺序建议

建议按以下顺序推进：

1. 在 runner 中引入 `project + patch merge`
2. 保留 legacy adapter，避免一次性打爆现有节点
3. 用 `IteratorNode` 做第一批迁移样板
4. 增加 graph build 阶段的 contract 依赖分析
5. 逐步迁移 planner、memory、verification 相关节点
6. 最后治理 subgraph wildcard

## 需要明确的工程原则

后续开发中建议明确以下原则：

1. 节点只能依赖契约声明的输入，不允许依赖完整状态树
2. 节点只返回 patch，不返回整棵状态
3. 节点私有执行态只能写入 `runtime.<node_id>` 区域
4. 共享业务结果只能写入 `shared.*` 或明确 scope
5. subgraph 默认不再允许 wildcard 透传
6. graph 组合必须经过 contract-level 依赖检查

## 总结

当前系统已经具备向契约化执行模型收敛的基础：

- 有 graph
- 有 registry
- 有 state contract
- 有 runner
- 有 checkpoint / resume

真正缺的是最后一步：

把 contract 从“声明和校验”变成“执行边界”。

一旦这一步完成，node 才会真正从“共享全局状态的执行片段”变成“可组合的能力单元”。

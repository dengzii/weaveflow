# Node State 解耦重构任务清单

## 目标

把当前“节点直接操作完整状态树”的执行模型，逐步重构为“节点基于 contract 读取输入视图，并返回受控 patch”的执行模型。

本清单按可落地顺序拆分，适合作为开发迭代任务使用。

## Phase 0: 基线确认

### Task 0.1

梳理当前节点 contract 覆盖率。

范围：

- registry 中所有已注册 node type
- 是否都能解析 `StateContract`
- 是否存在 wildcard 或明显过宽 contract

涉及文件：

- `registry.go`
- `registry_state_contracts.go`
- 各 module 文件

验收标准：

- 列出所有 node type 的 contract 状态
- 标出没有 contract 的节点
- 标出 wildcard 节点

### Task 0.2

梳理当前 runner 如何执行节点和校验 contract。

涉及文件：

- `runtime/graph_runner.go`
- `graph_runner_graph.go`
- `runtime/contract_validation.go`

验收标准：

- 明确当前 full state 传递路径
- 明确 contract 校验发生在哪个执行阶段

## Phase 1: 建立执行边界

### Task 1.1

新增状态投影能力。

目标：

- 根据节点 contract 的读路径，从 full state 投影出 input view

建议新增文件：

- `runtime/state_project.go`

建议接口：

```go
func ProjectStateByContract(full State, contract NodeIOContract) State
```

建议能力：

- 支持 root shared path
- 支持 `scopes.<scope>.*`
- 支持 `runtime.<node_id>.*`
- 支持空 contract
- 支持 wildcard contract

验收标准：

- 针对不同 contract 的输入视图有单元测试
- 未声明路径不会出现在 input view 中

### Task 1.2

新增 patch merge 能力。

目标：

- 节点执行后只返回 patch
- runner 根据写 contract 合并回 full state

建议新增文件：

- `runtime/state_patch.go`

建议接口：

```go
func MergePatchByContract(full State, patch State, contract NodeIOContract) (State, error)
```

建议能力：

- 只允许写声明过的路径
- required write path 缺失时可报错
- 支持 merge 策略扩展

验收标准：

- 合法 patch 能正确写回
- 非法写入被拒绝
- required path 缺失有测试覆盖

### Task 1.3

引入节点执行适配层。

目标：

- 兼容老节点 `Invoke(ctx, fullState) -> fullState`
- 给新 runner 提供统一的 patch 语义

建议新增文件：

- `runtime/node_execution.go`

建议接口：

```go
type ExecutableNode interface {
	Execute(ctx context.Context, input State) (State, error)
}
```

建议补充：

```go
type LegacyNodeAdapter struct {
	Inner nodes.Node[State]
}
```

验收标准：

- 老节点通过 adapter 仍可执行
- adapter 输出的是 patch，而不是直接透传 full state

### Task 1.4

runner 切换为 `project -> execute -> merge`。

目标：

- runner 不再直接把完整状态传给节点作为最终执行模型

涉及文件：

- `runtime/graph_runner.go`

改造点：

- 执行前按 contract 生成 input view
- 节点执行输出 patch
- runner merge patch 到 full state
- 继续保留现有 validate 作为安全网

验收标准：

- 原有主流程测试仍通过
- contract 校验仍有效
- 执行链路能在日志中看出 input/patch/full state 三阶段

## Phase 2: 明确 contract 模型

### Task 2.1

统一 contract 转换结构。

目标：

- 把当前写 contract 扩展为读写 contract

建议新增或调整结构：

```go
type NodeIOContract struct {
	ReadPaths     []string
	WritePaths    []string
	RequiredReads []string
	RequiredWrites []string
	WildcardRead  bool
	WildcardWrite bool
}
```

涉及文件：

- `graph_runner_graph.go`
- `runtime/contract_validation.go`

验收标准：

- contract 转换逻辑统一
- read/write 路径能分别表达

### Task 2.2

增强 contract 校验。

目标：

- 不只校验 undeclared write
- 还要校验 required read 和 required write

涉及文件：

- `runtime/contract_validation.go`

验收标准：

- 新增 required read 缺失测试
- 新增越权读测试
- 新增越权写测试

## Phase 3: 固定状态分区

### Task 3.1

明确状态分区规范并在代码中收口。

目标：

- 共享业务状态统一到 `shared.*`
- 作用域状态统一到 `scopes.<scope>.*`
- 节点私有执行态统一到 `runtime.<node_id>.*`
- 平台保留状态统一到 `internal.*`

涉及文件：

- `runtime/state.go`
- `runtime/contract_validation.go`
- 节点相关 contract resolver

验收标准：

- 新节点 contract 不再直接写 root 任意字段
- 文档中有状态分区说明

### Task 3.2

为现有 contract 做路径规范化清理。

目标：

- 清理歧义路径
- 降低直接依赖 root 裸字段的情况

涉及文件：

- `registry_state_contracts.go`
- 各 module 的 `resolve*StateContract`

验收标准：

- contract path 风格统一
- 不再新增不受控路径写入

## Phase 4: 迁移节点

### Task 4.1

迁移 `IteratorNode`。

目标：

- 让 iterator 成为第一个真正基于 input view 和 patch 的节点

涉及文件：

- `nodes/iterator.go`
- `registry_state_contracts.go`

验收标准：

- iterator 不再依赖 full state
- 仅输出自己的 runtime patch
- 现有 iterator 测试通过

### Task 4.2

迁移 memory 相关节点。

范围：

- `MemoryRecallNode`
- `MemoryWriteNode`

目标：

- 显式依赖 memory 相关输入
- 输出集中到声明路径

验收标准：

- memory 相关节点 patch 化
- 不再顺手修改非声明状态

### Task 4.3

迁移 planner / verifier / observation 相关节点。

范围：

- `PlannerNode`
- `VerifierNode`
- `ObservationRecorderNode`

目标：

- 将“依赖前驱节点”改成“依赖共享数据能力”

验收标准：

- 节点 contract 更聚焦
- 节点在不同图中复用时不需要固定前驱

### Task 4.4

迁移 LLM / Tools / ContextAssembler 相关节点。

范围：

- `LLMNode`
- `ToolsNode`
- `ContextAssemblerNode`

目标：

- conversation scope 输入输出边界清晰
- 避免对完整会话状态的隐式耦合

验收标准：

- scope 内状态更新可控
- 工具循环状态不再依赖全局可见性

## Phase 5: 静态组合能力

### Task 5.1

在 graph build / validate 阶段增加 contract 依赖分析。

目标：

- 节点组合问题在运行前暴露

涉及文件：

- `graph.go`
- `registry.go`

分析内容：

- 节点 read path 是否有来源
- 多节点写同一路径是否冲突
- contract 是否过宽

验收标准：

- 图构建时能报缺失依赖
- 图构建时能给出明显冲突警告

### Task 5.2

产出组合诊断信息。

目标：

- 给使用者明确提示为什么图不可组合

建议输出：

- 缺失输入字段列表
- 多写入冲突列表
- wildcard 节点风险提示

验收标准：

- validate 输出具备可读性
- 错误信息可直接定位到 node id 和 path

## Phase 6: 治理 subgraph

### Task 6.1

保留兼容型 passthrough subgraph。

目标：

- 不打断现有图运行

涉及文件：

- `nodes/subgraph.go`
- `registry_state_contracts.go`

验收标准：

- 兼容老图
- 明确标记高风险模式

### Task 6.2

新增 mapped subgraph。

目标：

- 显式输入映射
- 显式输出映射

建议能力：

- `input_map`
- `output_map`

验收标准：

- 子图调用不再依赖 wildcard
- 子图可以作为受控组合单元复用

## 测试任务

### Task T1

为状态投影补充单元测试。

覆盖：

- 普通 shared path
- scope path
- runtime path
- wildcard
- 空 contract

### Task T2

为 patch merge 补充单元测试。

覆盖：

- 合法 merge
- 越权写入
- required 缺失
- 嵌套 map merge

### Task T3

为 runner 新执行链路补充集成测试。

覆盖：

- legacy node 适配执行
- contract node 执行
- resume / continue 行为不回归

### Task T4

为 graph contract 依赖分析补充测试。

覆盖：

- 缺失输入
- 冲突写入
- wildcard 警告

## 推荐实施顺序

建议严格按以下顺序推进：

1. Phase 1.1
2. Phase 1.2
3. Phase 1.3
4. Phase 1.4
5. Phase 2.1
6. Phase 2.2
7. Phase 3.1
8. Phase 3.2
9. Phase 4.1
10. Phase 4.2
11. Phase 4.3
12. Phase 4.4
13. Phase 5.1
14. Phase 5.2
15. Phase 6.1
16. Phase 6.2

## 第一批建议直接开工的任务

如果只做最小可见收益，建议本周先落这 4 个任务：

1. `Task 1.1` 新增状态投影
2. `Task 1.2` 新增 patch merge
3. `Task 1.3` 新增 legacy adapter
4. `Task 1.4` runner 切换执行链路

这 4 个任务完成后，系统就已经从“contract 只做校验”进入“contract 开始参与执行”。

## 里程碑定义

### Milestone A

runner 已支持 input view 和 patch merge，旧节点仍可运行。

### Milestone B

至少一个内置节点完成 patch 化迁移，并有样板实现。

### Milestone C

graph 构建阶段能发现 contract 依赖缺失和冲突。

### Milestone D

subgraph 不再默认依赖 wildcard 透传。

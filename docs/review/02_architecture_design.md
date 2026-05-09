# 2. 架构设计审查

> 评级：**优秀**

## 统一架构模式：基于图的 DAG 执行 + 插件系统

WeaveFlow 采用 **LangGraph**（通过 `smallnest/langgraphgo`）作为底层执行引擎，在此基础上构建高层抽象。

### 双层图结构

- **Graph 层** (`graph.go`): LangGraph 的薄封装，提供节点与边管理
- **Runtime 层** (`runtime/graph_runner.go`): 编排执行，支持检查点/恢复语义

### 插件模块系统

每个 `*_module.go` 文件注册：
- 状态字段定义（Schema）
- 节点类型定义（构建函数 + 状态契约）
- 条件处理器（边路由逻辑）

```go
// registry.go — 模块注册
RegisterSessionBootstrapModule(r)
RegisterIntentModule(r)
RegisterOrchestrationModule(r)
RegisterMemoryModule(r)
RegisterContextModule(r)
RegisterExecutionModule(r)
RegisterVerificationModule(r)
RegisterSafetyModule(r)
RegisterReplannerModule(r)
```

## 模块耦合度分析

### 低耦合实现手段

1. **接口化依赖** (`runtime/runner_types.go`):
   - `ExecutionStore` — 抽象执行/步骤存储
   - `CheckpointStore` — 抽象检查点持久化
   - `EventSink` — 抽象事件发布
   - `ArtifactStore` — 抽象制品管理
   - `RunnerGraph` — 抽象图结构
   - `Node[S any]` — 泛型节点接口

2. **插件注册模式**: 新节点类型/条件无需修改核心注册表

3. **模块间零直接依赖**: 各模块仅依赖 `Registry` 接口和 `BuildContext`

### 潜在耦合问题

- **BuildContext 耦合**: 所有模块依赖共享的 `BuildContext{Model, Memory, Tools}`
- **状态字段字符串耦合**: 模块隐式依赖状态字段名 (`StateKeyIntent`, `StateKeyMemory`)
- **图拓扑硬编码**: `neo.go` 中图结构手动组装，非声明式

## 数据流清晰度

### 用户输入 → 输出完整流程

```
HTTP 请求 (ChatRequest)
  ↓
Server.Chat() [internal/neo/server.go]
  → 解析请求
  → 创建 Graph, Stores, Runner
  → 创建初始 State
  ↓
runner.Start(ctx, initialState)
  → 验证 Runner 配置
  → 创建 RunRecord
  → 编译图 → langgraph.StateRunnable
  → execute(ctx, run, state, entryPoint)
    ↓
  逐节点执行:
    → beforeNode() — 创建 Step, 保存检查点
    → node.Invoke() — 执行用户代码
    → OnGraphStep() — 保存检查点, 校验契约
    → 更新 RunRecord
  ↓
SessionBootstrap → MemoryRecall → Router → LLM/Tools → Verifier → Finalizer
  ↓
最终状态 → 序列化 → SSE 事件流 → 客户端
```

## 状态管理一致性

### 状态架构

```go
type State map[string]any  // 根级别业务数据

// 保留命名空间（__wf_ 前缀）:
__wf_conversation  // 对话消息（由 ConversationExtension 管理）
__wf_scopes        // 作用域状态
```

### 状态组织

| 层级 | 职责 |
|------|------|
| 根级别 | 用户定义的业务数据 |
| `__wf_conversation` | 对话消息、最终答案、迭代计数 |
| `__wf_scopes` | 按执行阶段隔离的状态 |

### 检查点/恢复

- 每个关键节点前后自动快照
- Codec 接口支持可插拔编码
- 检查点包含：运行时元数据、业务状态、对话、作用域、制品引用

### 契约校验

- 每节点状态契约（输入/输出声明）
- 节点执行后自动校验
- 三种模式：Off / Warn / Strict

### 优势

- ✅ 集中式状态类型定义
- ✅ 基础设施与业务数据分离
- ✅ 作用域隔离支持多阶段执行
- ✅ 完整状态历史（检查点链）

### 不足

- ✗ 字符串键 map 允许运行时类型错误
- ✗ Schema 定义分散在各模块中
- ✗ 无编译期状态形状校验

## 业务逻辑与 HTTP 分离

### HTTP 层职责 (`internal/neo/server.go`)

仅负责：请求解析、Graph/Runner 实例化、Store 初始化、SSE 流式传输

**不负责**：Agent 逻辑、工具执行、路由决策

### 业务逻辑位置 (`nodes/` 包)

每个节点类型封装单一职责：SessionBootstrap、IntentAnalyzer、MemoryRecall、OrchestrationRouter、Planner、LLMNode、ToolCallNode、Verifier、Finalizer、MemoryWrite 等。

## 模块可替换性

| 层 | 可替换性 | 说明 |
|----|---------|------|
| 存储层 | **优秀** | 全接口化：ExecutionStore, CheckpointStore, EventSink, ArtifactStore |
| 工具层 | **优秀** | Tool struct + map 注册，极易扩展 |
| 模型层 | **中等** | 依赖 langchaingo `llms.Model` 接口，需适配器包装 |
| 图运行时 | **困难** | 紧耦合 langgraph 的 `StateRunnable[T]` 类型 |

## 插件/扩展支持

### Registry 模式 (`registry.go`)

```go
type Registry struct {
    StateFields map[string]StateFieldDefinition  // 状态字段插件
    NodeTypes   map[string]NodeTypeDefinition    // 节点类型插件
    Conditions  map[string]ConditionDefinition   // 条件插件
}
```

### 扩展点

1. **自定义模块**: 创建 `RegisterCustomModule()` 注册新节点、条件、字段
2. **自定义节点**: 实现 `Node[State]` 接口
3. **自定义存储**: 实现 Store 接口，注入 GraphRunner
4. **自定义工具**: 创建 Tool struct，添加到 `BuildContext.Tools`
5. **配置驱动**: DSL 支持声明式图定义（JSON/YAML）

## 架构评价总结

| 方面 | 评级 | 说明 |
|------|------|------|
| 架构模式 | 优秀 | 清晰的图 DAG 模式 + langgraph |
| 模块耦合 | 良好 | 插件注册表解耦；字符串状态耦合是小问题 |
| 数据流 | 优秀 | HTTP → Graph → Nodes → State → Storage 分离清晰 |
| 状态管理 | 良好 | 集中式 State；检查点/恢复完善；类型安全不足 |
| 业务逻辑分离 | 优秀 | HTTP 层极薄；逻辑全在节点中 |
| 存储可替换性 | 优秀 | 全接口化 |
| 插件系统 | 优秀 | Registry 支持 10+ 个可插拔模块 |
| 配置驱动 | 良好 | DSL 支持声明式图；节点在 neo.go 中硬编码 |

## 改进建议

1. **图定义 DSL 驱动**: 将 `neo.go` 中硬编码的图结构改为配置文件驱动
2. **状态类型安全**: 考虑代码生成或泛型方案替代 `map[string]any`
3. **模型抽象层**: 包装 langchaingo 依赖为内部接口
4. **模块动态发现**: 将 RegisterModule 调用从硬编码改为自动发现

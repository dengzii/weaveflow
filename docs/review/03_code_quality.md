# 3. 代码质量审查

> 评级：**中等**

## 命名清晰度

### 正面发现

- 包命名描述性好：`nodes`, `runtime`, `tools`, `dsl`
- 类型命名清晰：`GraphRunner`, `LLMNode`, `PlannerNode`, `FileExecutionStore`
- 函数命名使用动作前缀：`NewLLMNode`, `BuildGraph`, `ResumeRun`

### 问题

| 位置 | 问题 | 建议 |
|------|------|------|
| `nodes/llm_node.go:39,56` | 接收者命名为 `L`，不符合 Go 惯例 | 改为 `n` 或 `llm` |
| `nodes/llm_node.go` 等 | `import fruntime "weaveflow/runtime"` 含义不明 | 使用更清晰的别名如 `wfrt` 或取消别名 |

## 函数长度

以下函数超过 50 行，应考虑拆分：

| 文件 | 函数 | 行数 | 职责过多 |
|------|------|------|---------|
| `nodes/planner.go` | `Invoke()` | 72 | 状态验证、目标解析、上下文收集、LLM 调用、响应解析、事件发布 |
| `nodes/context_assembler.go` | `Invoke()` | 57 | 消息处理、记忆包含、编排构建、制品保存 |
| `runtime/graph_runner.go` | `resumeExistingRun()` | 54 | 状态合并/恢复 + RunRecord 更新 |
| `runtime/graph_runner.go` | `execute()` | 47 | 图编译、回调、执行、错误处理 |
| `runtime/snapshot_codec.go` | `RestoreStateSnapshot()` | 46 | 多路径恢复 |

## 单一职责

### 违反 SRP 的主要类型

**GraphRunner** (`runtime/graph_runner.go:16-29`) — 9+ 个职责：
- 执行编排、运行管理、检查点处理、制品存储、状态差异、事件发布、断点匹配...
- **建议**: 拆分为 RunManager, CheckpointManager, ArtifactManager, ExecutionOrchestrator

**LLMNode** (`nodes/llm_node.go:19-24`) — 混合职责：
- LLM 调用、工具管理、制品创建、事件记录、状态变更
- `buildLLMPromptArtifact`, `buildLLMResponseArtifact`, `redactToolCalls` 应独立

**PlannerNode** (`nodes/planner.go`) — 混合职责：
- LLM 调用、响应解析、计划规范化、状态变更、上下文收集

## 重复逻辑

### 1. nil+TrimSpace 模式（大量重复）

出现在 `context_assembler.go`, `context_reducer.go`, `finalizer.go`, `intent_analyzer.go`, `memory_recall.go`, `memory_write.go`, `observation_recorder.go` 中：

```go
func (n *SomeNode) effectiveSomePath() string {
    if n == nil || strings.TrimSpace(n.SomePath) == "" {
        return defaultSomePath
    }
    return strings.TrimSpace(n.SomePath)
}
```

**建议**: 提取为共享工具函数 `effectiveString(value, defaultValue string) string`

### 2. "BestEffort" 制品保存（60+ 处）

```go
_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.prompt", payload)
```

遍布 `nodes/llm_node.go`, `nodes/planner.go`, `nodes/context_assembler.go` 等。

### 3. 文件路径操作模式

`runner_store.go` 中多处相同的 `os.ReadDir` + `os.IsNotExist` 检查模式。

### 4. Run 记录状态变更

`completeRun`, `cancelRun`, `pauseRun`, `failRun` 中相似的字段更新和事件发布模式。

## 过度封装

| 位置 | 问题 |
|------|------|
| `runtime/state.go` | 状态访问多层抽象：State → Namespaces → Scopes → Infrastructure keys |
| `runtime/snapshot_codec.go` | 三层嵌套编解码：StateSnapshot → GraphState → json.RawMessage |
| `graph.go` | Graph struct 40+ 方法，可考虑拆分为独立 Builder |
| `runtime/` | 10 个接口中多数只有单一实现 |

## 魔法数字/字符串

| 文件 | 位置 | 问题 |
|------|------|------|
| `tools/file.go:15-18` | `64*1024`, `256*1024`, `100`, `500` | 文件读取限制无说明 |
| `nodes/llm_node.go:68` | `0.8` | 硬编码温度值 |
| `nodes/llm_node.go:71` | `10000` | 硬编码最大 token 数 |
| `nodes/context_assembler.go:311` | `3` | 硬编码显示步骤数 |

**建议**: 提取为命名常量或可配置参数。

## 错误处理质量

### 正面模式

- `fmt.Errorf("...%w", err)` 包装上下文（`nodes/planner.go`, `graph.go`）
- 操作前 nil 检查

### 问题

| 严重度 | 问题 | 数量 |
|--------|------|------|
| 高 | `_, _ = SaveJSONArtifactBestEffort()` 静默吞错 | 60+ |
| 高 | `nodes/node.go:27` 生产代码使用 `panic()` | 1 |
| 中 | 错误处理策略不一致（有的包装有的不包装） | 30+ |

## 日志质量

### 正面

- 结构化日志使用 zap
- 上下文字段丰富：`runLogFields`, `stepLogFields`, `stateSummaryFields`
- 日志级别使用恰当：Info 状态转换、Debug 检查点、Error 失败

### 问题

- `runner_store.go` 完全没有日志
- `FileCheckpointStore.Save` 无检查点持久化日志
- `LoggerEventSink` 输出 `zap.ByteString("payload", event.Payload)` — JSON 以字节串形式输出，可读性差

## 注释质量

### 问题

| 类型 | 说明 |
|------|------|
| 废话注释 | `nodes/node.go:12-23` 字段名已自说明，注释多余 |
| 缺失注释 | `planner.go:342-393` `normalizePlannerResponse` 15+ 变换无注释 |
| 中文注释 | `nodes/planner.go:19-92` 70+ 行中文系统提示，降低代码审查可访问性 |
| 缺少原理注释 | `snapshot_codec.go` 遗留布局检查无解释 |

## 其他质量问题

| 问题 | 位置 |
|------|------|
| 接收者命名不一致 | `(L *LLMNode)` vs `(r *GraphRunner)` vs `(g *Graph)` |
| 重复克隆函数 | `clonePlannerStrings` vs `cloneStrings` 功能相同 |
| 状态隐式变更 | `conversation.UpdateMessage()` 隐式修改状态 |
| 大量类型断言 | `switch typed := value.(type)` 模式遍布多处 |

## 关键修复项

### 快速修复
1. 提取 nil+trim 模式为辅助函数
2. 替换 60+ 处 `_, _ = SaveArtifact()` 为适当错误处理
3. 拆分 `graph_runner.go` 的 `execute()` 和 `planner.go` 的 `Invoke()` 为小函数
4. 移除 `nodes/node.go:27` 的 panic

### 较大重构
1. 拆分 GraphRunner 为多个协作类型
2. 提取重复文件操作模式为 FileOps 辅助
3. 简化状态编解码层次
4. 文档化并提取魔法数字

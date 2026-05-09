# 7. 并发与异步审查

> 评级：**中等**

## Goroutine 管理

### 已发现的 Goroutine

| 位置 | 用途 | 状态 |
|------|------|------|
| `nodes/tool_call_node.go:65-74` | 并行工具执行 | ✅ WaitGroup 追踪，参数正确传入闭包 |
| `internal/neo/server.go:143-147` | 图执行 | ✅ 通道同步，无泄漏风险 |
| `llama_cpp/llama_cpp.go:264-273` | 模型生成 | ⚠️ `zap.Error(err)` 调用错误（应为 `logger.Error`） |

### 问题

- 工具并行执行无 goroutine 数量限制
- `llama_cpp.go:269` — `zap.Error(err)` 是字段构造器非日志方法

## 通道使用

### 严重问题：事件通道静默丢弃

**位置**: `internal/neo/event_stream.go:33-65`

```go
func (s *ChannelEventSink) Publish(_ context.Context, event fruntime.Event) error {
    select {
    case s.ch <- event:
    default:  // 静默丢弃事件！
    }
    return nil
}
```

- buffer=256 满时事件静默丢弃
- 调用方不知道事件丢失
- 影响可观测性而非正确性

### 正确的通道模式

- `internal/neo/server.go:140` — 结果通道 `make(chan runResult, 1)` ✅ 缓冲防泄漏
- SSE 事件循环正确检查通道关闭 `event, ok := <-channelSink.Events()` ✅
- `llama_cpp.go:265-266` — defer close 两个通道 ✅

## Context 取消

### 上下文创建与传播

**Neo 服务器** (`internal/neo/server.go:128-133`):
```go
ctx, cancel := context.WithCancel(c.Request.Context())
s.mu.Lock()
s.runner = runner
s.cancelFn = cancel
s.mu.Unlock()
```
- ✅ 正确创建带取消的 context
- ✅ cancel 函数在 mutex 保护下存储
- ⚠️ **无超时**（无限执行可能）

**客户端断开处理** (`server.go:159-164`):
```go
case <-clientGone:
    cancel()
    s.mu.Lock()
    s.cancelFn = nil
    s.mu.Unlock()
```
- ✅ 客户端断开时正确取消

### Cancel 请求机制

**位置**: `runtime/graph_runner.go:429-441`

```go
func (r *GraphRunner) Cancel(ctx context.Context, runID string) error {
    run.CancelRequested = true  // 设置标志
    r.ExecutionStore.UpdateRun(ctx, run)
}
```

**检查点** (`runtime/graph_runtime.go:101-108`):
```go
if e.run.CancelRequested {
    e.pending = &runnerPendingControl{kind: runnerControlCancel}
    return ctx, &langgraph.NodeInterrupt{...}
}
```

**问题**:
- 设置标志但不调用 `context.CancelFunc`
- 依赖轮询（每个节点边界检查）而非事件驱动
- **取消请求有延迟**——直到下一个节点边界才生效
- 节点执行中无法取消

## Mutex 使用

### Mutex 位置评估

| 位置 | 类型 | 评估 |
|------|------|------|
| `runtime/graph_runtime.go:54` | `sync.Mutex` | ✅ 保护 active/pending/run/lastState/artifacts |
| `internal/neo/server.go:35` | `sync.RWMutex` | ✅ 读写分离正确 |
| `runtime/runner_store.go` | `sync.Mutex` ×3 | ✅ 各 Store 独立锁 |
| `memory/in_memory_repository.go` | `sync.RWMutex` | ✅ 并发读支持 |
| `internal/server/model_manager.go` | `sync.RWMutex` | ❌ **存在竞态条件** |
| `llama_cpp/llama_cpp.go:283-284` | `sync.Mutex` | ✅ 整个生成序列持锁 |

## 竞态条件

### 严重：ModelHub.Generate 竞态

**位置**: `internal/server/model_manager.go:65-74`

```go
func (m *ModelHub) Generate(ctx context.Context, id string, ...) {
    m.mu.RLock()
    model, ok := m.models[id]  // 获取模型引用
    m.mu.RUnlock()              // 释放锁
    // ↑ 另一个 goroutine 可在此时调用 Release() 删除模型
    return model.Generate(ctx, prompt, options)  // 使用已被删除的模型！
}
```

**竞态**: RLock 释放后、Generate 调用前，另一个 goroutine 可能通过 `Release()` 删除该模型。

**可能性**: 高负载下高
**影响**: 崩溃或未定义行为
**修复**: 保持 RLock 直到 Generate 返回，或实现引用计数

### 其他安全区域

- `graphRunnerExecution` — 所有共享状态访问在 mutex 保护下 ✅
- Neo 服务器 Config — 在锁下复制后使用 ✅
- State 传值给节点（CloneState），无共享 ✅

## 任务取消机制

**实现**: 标志位 + 节点边界检查

```
Cancel() → 设置 CancelRequested = true
                    ↓
beforeNode() → 检查 CancelRequested → NodeInterrupt
```

**问题**:
- 取消仅在节点边界生效，不在节点内
- 不使用 Go 的 `context.CancelFunc` 做即时取消
- 工具执行中无法取消
- 如果工具挂起，阻塞整个节点

## 超时机制

| 组件 | 超时 | 状态 |
|------|------|------|
| LLM 调用 (`nodes/llm_node.go:63-71`) | **无** | ❌ 可能无限挂起 |
| 工具执行 (`nodes/tool_call_node.go:143`) | **无** | ❌ 单个慢工具阻塞整组 |
| Web Fetch (`tools/web_fetch.go:19,81`) | 30s | ✅ `http.Client{Timeout: fetchTimeout}` |
| Llama C++ (`llama_cpp/model.go:32-33`) | **无**（响应父 context） | ⚠️ |
| 全局执行 (`internal/neo/server.go`) | **无** | ❌ 图执行可能无限运行 |

## 并发数量限制

### 并行工具执行 — 无限制

```go
if t.Parallel {
    var wg sync.WaitGroup
    wg.Add(len(toolCalls))  // 无限制！
    for index, toolCall := range toolCalls {
        go func(...) { ... }(index, toolCall)
    }
    wg.Wait()
}
```

**无 semaphore 或 worker pool**。100 个工具调用 = 100 个 goroutine。

**建议**: 使用 `semaphore.NewWeighted()` 或缓冲通道限制并发。

## 状态一致性

### 安全的设计

- `State map[string]any` 非线程安全，但通过克隆模式保护
- 每个节点接收状态副本：`state.CloneState()`
- `graphRunnerExecution` 所有状态变更在 mutex 保护下
- Artifact 追加在锁保护下

### 轻微风险

- `OnGraphStep` 中多个字段在 unlock 和 clone 之间读取

## 问题汇总

### 严重（需立即修复）
1. **ModelHub.Generate 竞态条件** — `model_manager.go:65-74`
2. **事件静默丢失** — `event_stream.go:42-46`
3. **LLM 调用无超时** — `llm_node.go:63-71`
4. **工具并行无限制** — `tool_call_node.go:64-75`

### 高（应修复）
5. Cancel 机制有延迟——仅节点边界检查
6. 工具执行无超时
7. 全局无执行超时
8. 工具 Handler 不检查 `ctx.Done()`

### 中（建议修复）
9. 事件通道丢弃应至少记录日志
10. `zap.Error(err)` 调用错误 — `llama_cpp.go:269`
11. 错误通道 buffer 过小 — `llama_cpp.go:248`

## 建议

1. **修复 ModelHub 竞态**: 保持读锁直到 Generate 返回或引用计数
2. **添加 context 超时**: `context.WithTimeout` 包装 runner.Start
3. **工具执行限制**: `semaphore.NewWeighted(maxConcurrentTools)`
4. **工具执行超时**: 每工具 context 超时
5. **修复事件丢失**: 返回 error 或记录丢弃日志
6. **改进取消**: 将服务器级 cancel 直接挂钩到执行 context

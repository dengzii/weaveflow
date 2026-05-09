# 6. 性能审查

> 评级：**中等偏差**

## 严重：N+1 查询问题

### GetStep 遍历所有 Run 目录

**位置**: `runtime/runner_store.go:195-208`

```go
func (s *FileExecutionStore) GetStep(_ context.Context, stepID string) (StepRecord, error) {
    runs, err := s.ListRuns(context.Background(), RunFilter{})  // 列出所有 runs
    for _, run := range runs {                                   // 遍历所有 runs
        path := s.stepPath(run.RunID, stepID)
        var step StepRecord
        if err := readRunnerJSONFile(path, &step); err == nil {  // O(n) 文件读取
            return step, nil
        }
    }
    return StepRecord{}, ErrRunnerRecordNotFound
}
```

**影响**: 每次 `GetStep` 调用读取所有 run 的 JSON 文件。100 个 run = 100+ 次文件系统操作。

**严重度**: 严重

### CheckpointStore.Load 线性扫描

**位置**: `runtime/runner_store.go:276-300`

```go
func (s *FileCheckpointStore) Load(_ context.Context, checkpointID string) (...) {
    runDirs, err := os.ReadDir(s.baseDir)  // 读取目录
    for _, runDir := range runDirs {       // 遍历所有 run 目录
        metaPath := filepath.Join(s.baseDir, runDir.Name(), checkpointID+".json")
        if err := readRunnerJSONFile(metaPath, &record); err == nil {
            payload, err := os.ReadFile(record.PayloadRef)  // 同步读取全部载荷
            return record, payload, nil
        }
    }
}
```

**影响**: 扫描所有 run 目录查找单个检查点，无索引查找。

**严重度**: 高

## 阻塞 I/O 问题

### SSE 事件循环阻塞

**位置**: `internal/neo/server.go:143-191`

```go
go func() {
    defer channelSink.Close()
    run, state, runErr := runner.Start(ctx, initialState)  // 同步阻塞调用
    done <- runResult{...}
}()

for {
    select {
    case event, ok := <-channelSink.Events():
        writeSSE(c, chatEvent)  // 每事件同步写入
    }
}
```

**影响**: 图执行同步运行在单 goroutine；SSE 事件逐个处理。

### EventSink 每事件独立文件操作

**位置**: `runtime/runner_store.go:350-363`

```go
func (s *FileEventSink) Publish(_ context.Context, event Event) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    return appendRunnerJSONLine(s.eventsPath(event.RunID), event)
    // appendRunnerJSONLine: 每次 open → marshal → write → close
}
```

**影响**: 高事件量时文件操作成为瓶颈。

**严重度**: 高

## 内存问题

### FileRepository 全量重写

**位置**: `memory/file_repository.go:43-95`

每次 Store 操作：
1. 读取所有内存条目
2. 全部写入临时文件
3. 删除整个原目录
4. 重命名临时目录

**影响**: 大量记忆数据时非常缓慢，无增量更新。

**严重度**: 高

### Artifact 列表无分页

**位置**: `runtime/artifact_store.go:96-125`

```go
func (s *FileArtifactStore) List(_ context.Context, runID string) ([]ArtifactRef, error) {
    files, err := os.ReadDir(dir)
    items := make([]ArtifactRef, 0, len(files))
    for _, file := range files {  // 遍历所有制品
        readRunnerJSONFile(...)   // 每个完整反序列化
    }
    sort.Slice(items, ...)        // 内存排序
}
```

**影响**: 1000 个制品 = 1000 次 JSON 反序列化 + 内存排序。无分页，无限制。

**严重度**: 高

### 对话历史无界增长

所有使用 `fruntime.Conversation(state, scope).Messages()` 的节点遍历完整历史，无窗口化或截断机制。

**严重度**: 中

## 重复计算

### 重复状态快照

**位置**: `runtime/graph_runtime.go:233-242`, `runtime/graph_runner.go:565-598`

每次 `OnGraphStep`:
1. `saveCheckpoint()` — 创建快照
2. `computeStateDiff()` — 再创建 2 个快照用于 diff

**影响**: 每个节点执行冗余创建 3+ 次 StateSnapshot。

**严重度**: 中

### 重复 `Conversation()` 调用

多个节点中同一函数内多次调用 `fruntime.Conversation(state, scope)`，每次重新从嵌套 map 构造对话状态。

**严重度**: 中

## 资源管理问题

### 并行工具执行无限制

**位置**: `nodes/tool_call_node.go:64-75`

```go
if t.Parallel {
    var wg sync.WaitGroup
    wg.Add(len(toolCalls))  // 无 goroutine 数量限制
    for index, toolCall := range toolCalls {
        go func(...) {      // 无界 goroutine 创建
            defer wg.Done()
            toolMessages[index] = t.executeToolCallMessage(ctx, toolCall)
        }(index, toolCall)
    }
    wg.Wait()
}
```

**影响**: LLM 生成 1000 个工具调用 = 1000 个 goroutine，无 semaphore 或 worker pool。

**严重度**: 中

### 事件通道静默丢弃

**位置**: `internal/neo/event_stream.go:37-47`

```go
func (s *ChannelEventSink) Publish(_ context.Context, event fruntime.Event) error {
    select {
    case s.ch <- event:
    default:  // 通道满时静默丢弃！
    }
    return nil
}
```

**影响**: buffer=256 满时事件被丢弃，无反压、无通知。

**严重度**: 中

### 无 Context 超时的阻塞 I/O

**位置**: `tools/file.go:89-122`

文件操作不使用 context 做取消：
```go
info, err := os.Stat(target)   // 可能无限阻塞
data, err := os.ReadFile(target) // 可能无限阻塞
```

**严重度**: 中

## 其他性能问题

| 问题 | 位置 | 严重度 |
|------|------|--------|
| 每次事件 JSON 序列化 | `graph_runner.go:709-730` | 低-中 |
| 断点匹配线性搜索 | `graph_runner.go:687-707` | 低 |

## 问题汇总

| 问题 | 严重度 | 类型 | 影响 |
|------|--------|------|------|
| N+1 Step 查找 | 严重 | I/O | O(n) 次读取/每次查找 |
| N+1 Checkpoint 加载 | 高 | I/O | 扫描全部 run 目录 |
| FileRepository 全量重写 | 高 | I/O | 每次 Store 完全重写 |
| EventSink 逐事件文件操作 | 高 | I/O | 每事件 open/write/close |
| Artifact 列表无分页 | 高 | 内存 | 全量反序列化 |
| 重复状态快照 | 中 | 计算 | 每节点 3+ 次快照 |
| 并行工具无限制 | 中 | Goroutine | 无 semaphore |
| 事件通道静默丢弃 | 中 | 数据丢失 | 无通知 |
| 对话历史无界增长 | 中 | 内存 | 历史无限增长 |
| 无 Context 超时 | 中 | 控制 | 无法取消阻塞 I/O |
| 每事件 JSON 序列化 | 低-中 | 计算 | 每事件分配 |
| 线性断点搜索 | 低 | 查找 | 每节点线性扫描 |

## 建议

### 高优先级
1. `GetStep`/`CheckpointStore.Load` 添加索引或路径直达查找
2. FileRepository 改为增量追加写入
3. EventSink 改为批量缓冲写入（定期 flush）
4. 并行工具执行添加 `semaphore.NewWeighted()`

### 中优先级
5. Artifact 列表添加分页
6. 复用 saveCheckpoint 的快照用于 computeStateDiff
7. 事件通道丢弃时记录日志
8. 对话历史添加窗口限制

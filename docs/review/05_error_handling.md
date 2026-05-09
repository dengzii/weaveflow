# 5. 错误处理审查

> 评级：**较差**

## 关键异常捕获

### 被吞没的错误（赋值给 `_`）

这是最令人担忧的类别——功能结果被丢弃：

| 文件 | 行号 | 代码 | 影响 |
|------|------|------|------|
| `debug/replay/server.go` | 218 | `_ = encoder.Encode(payload)` | HTTP 响应 JSON 编码错误被忽略 |
| `debug/replay/server.go` | 229 | `_, _ = w.Write(artifact.Data)` | 制品写入错误被忽略 |
| `graph.go` | 72, 81 | `_ = g.AddGlobalListener(...)`, `_ = f.Close()` | 监听器注册和文件关闭错误被忽略 |
| `internal/neo/server.go` | 199 | `_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)` | SSE 写入错误被忽略 |
| `memory/file_repository.go` | 57 | `_ = os.RemoveAll(tempDir)` | 清理错误被忽略 |
| `runtime/runner_store.go` | 433 | `_ = temp.Close()` | 临时文件关闭错误被忽略 |
| `tools/file.go` | 224 | `_ = f.Close()` | 文件关闭错误被忽略 |

### "BestEffort" 大面积吞错（60+ 处）

遍布所有节点实现：

```go
_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.prompt", payload)
_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.error", map[string]any{...})
_ = fruntime.PublishRunnerContextEvent(ctx, ...)
```

涉及文件：`nodes/llm_node.go`, `nodes/planner.go`, `nodes/context_assembler.go`, `nodes/verifier.go` 等。

## 错误分类 — 缺失

代码**不区分**以下错误类型：
- 用户错误（无效输入）
- 网络错误（API 失败）
- 系统错误（文件 I/O、权限）
- 程序错误（逻辑 bug）

**仅有 2 个哨兵错误**：
- `runtime/runner_types.go:12`: `ErrRunnerRecordNotFound`
- `runtime/runner_context.go:16`: `ErrArtifactRecorderUnavailable`

示例——不可区分的错误：
```go
// internal/server/api_infer.go — 两者都是用户错误但未分类
return errors.New("only support stream chat yet")
return errors.New("messages are required")
```

## 统一错误模型 — 不一致

代码中存在三种不同的错误处理模式：

**模式 1**: `errors.New()` — 无上下文（最常见）
```go
return errors.New("model path is required")
```

**模式 2**: `fmt.Errorf() + %w` — 带上下文（最佳实践，但使用不一致）
```go
return fmt.Errorf("load graph definition from %q: %w", path, err)
```

**模式 3**: 裸 error 透传 — 丢失上下文（危险）
```go
if err := r.modelManager.Load(...); err != nil {
    return err  // 上下文丢失——这是什么操作？
}
```

**关键路径缺少上下文包装**（`runtime/graph_runner.go`）:
```go
run, err := r.ExecutionStore.GetRun(ctx, runID)
if err != nil {
    return RunRecord{}, nil, err  // 应该: "resume run %q: %w"
}
```

## 错误码 — 未实现

数据模型中**已定义**但**从未使用**的错误码字段：

```go
// runtime/runner_types.go:86, 102
type RunRecord struct {
    ErrorCode    string `json:"error_code,omitempty"`    // 已定义
    ErrorMessage string `json:"error_message,omitempty"` // 已定义
}
```

无标准化错误码系统供 API 消费者使用。

## 用户友好错误消息

### HTTP 处理问题

**`internal/server/route_resolver.go:199-230`**:
- 所有响应都返回 HTTP 200（包括错误）
- 错误码使用随意数字：40001, 300, 500
- 原始错误消息直接返回给用户

```go
func onHandlerFuncErr(ctx *gin.Context, err error) {
    ctx.JSON(http.StatusOK, CommonResponse{
        Code: 500,
        Msg:  err.Error(),  // 原始错误消息暴露给用户
    })
}
```

**`debug/replay/server.go:206-209`**:
```go
// 基于字符串的脆弱分类逻辑
if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "required") {
    status = http.StatusNotFound
}
```

### 错误消息一致性问题

- 部分错误说 "nodes is nil"（应为 "node"）
- 大小写和标点不一致

## 开发者日志 — 稀少

**整个生产代码仅 2-3 处 logger 调用**：

```go
// runtime/graph_runner.go:80
logger.Info("run started", ...)

// runtime/graph_runner.go:645
logger.Error("run failed", ...)

// runtime/graph_runtime.go:344
logger.Error("nodes failed", ...)
```

大量关键路径**没有错误日志**：
- Resume 错误
- 文件操作错误
- 内存仓库错误
- 节点调用错误

## 生产代码中的 Panic

**危险的 panic**:
```go
// nodes/node.go:27
panic("NodeID is empty " + n.Name())
```
应该返回 error 而非 panic。

**路由设置 panic**（`internal/server/server.go:57, 80, 90`）— 初始化阶段可接受。

## 良好模式 vs 不良模式

### 良好模式
- 原子文件写入（`runner_store.go:419-440`）
- 哨兵错误使用（`errors.Is(err, ErrRunnerRecordNotFound)`）
- 部分路径有上下文包装

### 不良模式
- "BestEffort" 异常吞没（40+ 处）
- 同一代码路径不一致的包装
- 忽略文件关闭错误
- 无错误恢复机制

## 风险汇总

| 类别 | 严重度 | 数量 | 影响 |
|------|--------|------|------|
| 吞没错误 | 高 | 50+ | 关键路径静默失败 |
| 缺少上下文包装 | 高 | 30+ | 生产问题难调试 |
| 无错误分类 | 中 | 全局 | 无法区分用户/系统错误 |
| 无错误码 | 中 | 全局 | API 错误处理薄弱 |
| 日志稀少 | 中 | 大部分路径 | 调试困难 |
| 生产 panic | 高 | 1 | 崩溃风险 |
| 文件关闭错误 | 中 | 5+ | 潜在资源泄漏 |

## 建议

### 立即修复
1. 审查 `SaveJSONArtifactBestEffort` 调用——确定失败是否应传播
2. 实现一致的 `fmt.Errorf` + `%w` 错误包装
3. 文件关闭操作添加错误处理
4. `nodes/node.go:27` panic 改为 return error

### 中期改进
5. 创建自定义错误类型（用户/系统/网络分类）
6. 实现集中式 HTTP 错误响应格式化
7. 所有错误路径添加结构化日志
8. 定义错误码体系供 API 消费者使用

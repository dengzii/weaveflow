# 9. 可观测性审查

> 评级：**部分完善**

## 日志分级

### 日志框架

使用 **Uber Zap** 结构化日志：

```go
// runtime/graph_runtime.go:15-19
var logger = zap.NewNop()
func SetLogger(l *zap.Logger) { logger = l }
```

### 日志级别使用

| 级别 | 位置数 | 用途 |
|------|--------|------|
| `logger.Info()` | 11 处 | 正常流程事件 |
| `logger.Debug()` | 3 处 | 检查点保存、状态差异、生成指标 |
| `logger.Warn()` | 2 处 | 重试、契约违反 |
| `logger.Error()` | 2 处 | 节点失败、Run 失败 |

### 结构化字段

`runtime/logging.go` 提供全面的字段构建器：
- `runLogFields()` — run_id, graph_id, graph_version, status, error info
- `stepLogFields()` — run_id, step_id, node_id, node_name, attempt, status
- `checkpointLogFields()` — checkpoint_id, node_id, stage
- `artifactLogFields()` — 制品元数据
- `stateSummaryFields()` — state_keys, state_scopes, conversation_messages

### 非结构化日志

`fmt.Println()` / `fmt.Printf()` 仅出现在 `examples/` 目录——可接受。

## 关键链路日志 — 优秀

### 图执行管道追踪

| 阶段 | 日志 | 字段 |
|------|------|------|
| Run 启动 | `logger.Info("run started")` | run_id, graph_id, entry_node_id, state |
| Run 执行 | `logger.Info("run executing")` | start_node, breakpoint_count, state |
| 节点调度 | `logger.Debug("nodes scheduled")` | step_id, node_id, node_name, attempt |
| 节点启动 | `logger.Info("nodes started")` | step info + state summary |
| 节点重试 | `logger.Info("nodes retry")` | attempt > 1 时记录 |
| 节点完成 | `logger.Info("nodes completed")` | checkpoint_after_id, state |
| 检查点保存 | `logger.Debug("checkpoint saved")` | checkpoint_id, payload_bytes |
| 状态变更 | `logger.Debug("state diff computed")` | change_count |
| Run 完成 | `logger.Info("run completed")` | status, state |
| Run 失败 | `logger.Error("run failed")` | error_code, error_message |

### 工具调用追踪

通过事件系统（非直接日志）：
- `EventToolCalled` — 工具被调用
- `EventToolReturned` — 工具返回
- `EventToolFailed` — 工具失败

### Token 用量追踪

`nodes/usage.go` 完整追踪：
- prompt_tokens, completion_tokens, total_tokens, reasoning_tokens, cached_tokens
- 按节点、按模型、按作用域聚合
- 发布 `EventLLMUsage` 事件

### LLM 生成性能指标

`llama_cpp/llama_cpp.go` 记录（DEBUG 级别）：
- Go 侧计时：tokenize, prefill, sample, decode, emit 等
- LLAMA 侧计时：prompt_eval_ms, eval_ms, sample_ms
- Token 计数：prompt_tokens, generated_tokens

## 错误上报

### 错误记录机制

- `RunRecord.ErrorCode` / `ErrorMessage` — 存储在 JSON 文件
- `StepRecord.ErrorCode` / `ErrorMessage` — 存储在 JSON 文件
- 发布 `EventRunFailed` 事件

### 错误类型追踪

- `compile_failed` — 图编译错误
- `config_failed` — 配置错误
- `node_failed` — 节点执行错误
- `interrupt_failed` — 中断处理错误

## 崩溃收集 — 缺失

### 现状

- **运行时无 `recover()` 调用**
- 服务器有 `gin.Recovery()` 中间件（仅 HTTP 层）
- `route_resolver.go` 中有裸 panic（初始化阶段）

### 缺失

- `GraphRunner.Start()` / `execute()` 无 panic recovery
- 节点执行无 panic recovery
- 事件发布无 panic recovery
- **运行时 panic 将导致整个系统崩溃**

## 性能指标 — 部分

### 已有

| 指标 | 覆盖 |
|------|------|
| Token 用量 | ✅ 全面（按节点/模型/作用域） |
| LLM 生成计时 | ✅ llama_cpp 详细 |
| Step 时间戳 | ✅ StartedAt/FinishedAt（可推导延迟） |

### 缺失

| 指标 | 状态 |
|------|------|
| 节点延迟（预计算） | ❌ 未记录 duration 字段 |
| 整体 Run 延迟 | ❌ 未记录 |
| 内存/CPU 指标 | ❌ |
| 吞吐量指标 | ❌ |
| 预算消耗指标 | ❌ 仅用于预算执行，不发布为指标 |

## 用户行为上下文 / 请求追踪

### Run 级别追踪 — 优秀

- 每个操作绑定 `RunID`（UUID）
- 每个步骤绑定 `StepID`（UUID）
- Run 内关联完美

### Context 传播

`runtime/runner_context.go` 提供：
- `WithRunnerEventPublisher()` — 事件发布
- `WithRunnerMetadata()` — 携带 RunID, StepID, NodeID, Attempt
- `WithRunnerArtifactRecorder()` — 制品记录

### 缺失

| 功能 | 状态 |
|------|------|
| OpenTelemetry 集成 | ❌ |
| HTTP Trace Header (X-Trace-ID) | ❌ |
| 父 Trace ID 传播 | ❌ |
| 跨服务关联 | ❌ |
| 用户会话追踪 (user_id, session_id) | ❌ |

## 版本/平台/设备标识 — 部分

### 已有

- `GraphRunner.GraphVersion` — 图版本（默认 "1.0"）
- `StateCodec.Version()` — 状态编码版本
- 每条日志包含 graph_version 字段

### 缺失

| 信息 | 状态 |
|------|------|
| 应用版本 | ❌ 无 git hash / 构建时间 |
| 平台信息 | ❌ 无 OS / 架构 / Go 版本 |
| 设备信息 | ❌ 无 hostname / pod name |
| 任务 ID | ❌ 无外部任务引用 |
| 进程 ID | ❌ 无 PID |

## 敏感信息保护 — 优秀

### 脱敏框架

`internal/redact/redact.go`（146 行）提供完整脱敏引擎。

**敏感键检测**:
```
authorization, auth_header, api_key, x_api_key,
access_token, refresh_token, password, secret,
cookie, set_cookie, private_key
```

**敏感内容标记**:
```
authorization:, bearer, x-api-key, api-key:, apikey:,
access_token, refresh_token, password=, password:,
secret=, secret:, cookie:, set-cookie:,
session=, token=, private_key, -----begin
```

**脱敏函数**:
- `redact.Path()` — 路径脱敏（仅保留文件名）
- `redact.Text()` — 扫描敏感标记，脱敏行
- `redact.JSONString()` — 解析 JSON 递归脱敏
- `redact.Any()` — 递归脱敏任何 Go 值

### 集成点

- `runtime/redaction.go` — 消息脱敏（Text, URL, Arguments, Content）
- `llama_cpp/llama_cpp.go:261` — `zap.String("prompt", redact.Text(prompt))`

## 汇总

| 维度 | 状态 | 说明 |
|------|------|------|
| 日志框架 | 优秀 | Zap 结构化日志 |
| 日志级别 | 良好 | Info/Warn/Debug/Error 使用恰当 |
| 关键链路日志 | 优秀 | 每个主要状态转换都有日志 |
| 错误记录 | 良好 | ErrorCode + ErrorMessage，持久化 |
| **Panic 恢复** | **严重缺失** | 运行时无 recover() |
| 性能指标 | 部分 | Token/LLM 指标好，缺节点延迟 |
| **分布式追踪** | **缺失** | 无 OpenTelemetry |
| **用户会话** | **缺失** | 无 user_id/session_id |
| **版本/平台** | **缺失** | 无构建版本/主机名 |
| 敏感数据保护 | 优秀 | 完整脱敏框架 |

## 建议

### 高优先级
1. 添加 panic recovery 到 `GraphRunner.execute()` 和节点执行
2. 添加构建版本信息（`-ldflags` 注入 git hash, 构建时间）
3. 添加节点延迟指标（预计算 duration 并记录）

### 中优先级
4. 集成 OpenTelemetry 追踪
5. 添加 user_id/session_id 到日志上下文
6. 添加 hostname/PID 到日志

### 低优先级
7. 添加内存/CPU 运行时指标
8. 添加吞吐量指标

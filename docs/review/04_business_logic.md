# 4. 业务逻辑审查

> 评级：**良好**

## 核心业务流程正确性

**文件**: `runtime/graph_runner.go`

### 执行流程分析

`Start()` 方法正确实现了：
- 执行前验证 Runner 配置
- 初始化 RunRecord（时间戳、状态追踪）
- 克隆初始状态防止外部变更
- 在适当阶段发布事件
- 状态转换：PENDING → RUNNING → COMPLETED/FAILED/PAUSED

### Resume 逻辑

`Resume()` 方法正确检查检查点可用性。`isContinuableRunStatus()` 允许从 COMPLETED/FAILED 状态恢复——这创建新执行上下文，RunID 被复用。

### 流程路径完整性

- ✅ 正常完成路径 (`completeRun`)
- ✅ 取消路径 (`cancelRun`)
- ✅ 失败路径 (`failRun`)
- ✅ 暂停/断点路径 (`pauseRun`)
- ✅ 中断处理 (`handleInterrupt`)
- ⚠️ **无显式超时处理**（委托给 context）

## 边界条件处理

### 状态合并 (`state_merge.go`) — 健壮

- 正确验证 scopes 输入格式
- 保留键检查（拒绝 `__wf_scopes`）
- 处理 nil 基础状态
- 递归 scope 合并
- 测试覆盖全面

### 契约校验 (`contract_validation.go`) — 正确

路径匹配处理三种场景：
1. **精确匹配**: `"shared.messages" == "shared.messages"`
2. **子路径覆盖**: `"shared.planner.status"` 被 `"shared.planner"` 覆盖
3. **父路径覆盖**: `"shared.planner"` 在声明 `["shared.planner.status", "shared.planner.steps"]` 时允许

空契约正确视为宽松（测试验证）。

### 迭代器节点 (`nodes/iterator.go`) — 无 Off-by-One 错误

```go
if nextIndex >= limit {  // 边界检查正确（不是 >）
    writeIteratorDoneState(...)
    return state, nil
}
runtimeState["is_last"] = nextIndex == limit-1  // 正确，不是 limit
```

- 初始迭代：`nextIndex=0`, `limit=min(len(items), MaxIterations)` → 处理 item 0 ✓
- `iteration = nextIndex + 1` → 用户友好的 1-indexed ✓
- 测试验证全部通过 ✓

### 预算守卫 (`nodes/cost_budget_guard.go`) — 安全

- `limit <= 0` 正确跳过未配置的限制
- `current >= limit` 正确标识超出
- 无效阈值（≤0 或 ≥1）默认为 0.8
- nil map 不会崩溃

## 异常分支处理

### LLM 节点失败 (`nodes/llm_node.go`)

```go
resp, err := L.model.GenerateContent(ctx, messages, ...)
if err != nil {
    // 保存错误制品，返回 error（传播到图）
    return state, err
}
if resp == nil || len(resp.Choices) == 0 {
    return state, errors.New("llm returned no choices")
}
```

**问题**：
- 节点级无重试逻辑——错误直接导致整个 Run 失败
- 工具调用前无验证（可能包含畸形调用）
- 如果 GenerateContent 成功但工具调用失败，迭代被浪费

### 工具调用失败 (`nodes/tool_call_node.go`)

```go
result, err := t.executeToolCall(ctx, toolCall)
if err != nil {
    result = "tool execution failed: " + err.Error()  // 优雅降级
}
```

- ✅ 工具错误转换为消息内容（让 LLM 知道失败了）
- ✅ 失败和成功都发布事件
- ✅ 单个工具失败不影响并行组

**工具不存在** (`tool_call_node.go:113`):
```go
tool, ok := t.Tools[toolCall.FunctionCall.Name]
if !ok {
    return "", fmt.Errorf("tool %q not found", toolCall.FunctionCall.Name)
}
```
正确行为：错误消息进入对话历史。但 Agent 可能无限循环调用不存在的工具。

## 输入验证

### API Graph (`internal/server/api_graph.go`) — 多层验证

验证序列：Instance 配置 → Graph 定义 → Run 请求 → 状态合并

**路径遍历安全**: `filepath.Join(a.baseDir, entry.Name())` 安全，因为 `entry.Name()` 来自 `os.ReadDir()`。

### Neo 服务器 (`internal/neo/server.go`) — 最小验证

```go
if strings.TrimSpace(req.Message) == "" {
    c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "message is required"})
    return
}
```

**缺口**: 无消息长度限制（可能导致 OOM 或 LLM API 超限）。

## 状态转换完整性

```
PENDING → RUNNING → [PAUSED ↔ RUNNING] → COMPLETED
                                        → FAILED
                                        → CANCELED
```

所有转换已覆盖：
- ✅ PENDING→RUNNING
- ✅ RUNNING→COMPLETED
- ✅ RUNNING→FAILED
- ✅ RUNNING→CANCELED
- ✅ RUNNING→PAUSED
- ✅ PAUSED→RUNNING

## 隐含假设

| 假设 | 风险 |
|------|------|
| State 克隆为深拷贝 | 自定义类型嵌入 State 不会克隆（仍引用原对象） |
| MessageContent 顺序不可变 | 低风险（append 保序） |
| 检查点阶段语义确定性 | 如果边条件依赖精确状态版本，旧检查点恢复可能失败 |
| 制品记录为 fire-and-forget | 正确——制品是可选上下文 |
| UUID 唯一性 | 极低风险 |

## 竞态条件

### 运行时锁保护 (`runtime/graph_runtime.go`) — 安全

- `sync.Mutex` 保护可变状态
- `CloneState()` 在持锁时执行
- 节点接收状态副本，不共享

### Neo 服务器并发 Chat — 有意设计

两个并发 Chat 请求之间：旧 runner 被取消，新 runner 替代。这是有意行为但未文档化。

## 多平台考虑

- 使用 `filepath.Join()` — 跨平台安全 ✅
- 无 `GOOS`/`GOARCH` 构建标签
- 无硬编码路径分隔符
- 文件权限 `0o644` 硬编码（敏感数据可能在多用户系统上泄露）

## 建议

1. **添加 LLM 重试逻辑** — 支持网络超时、速率限制等瞬态失败
2. **Neo 端添加消息长度限制**
3. **文档化检查点 Codec 不可变性要求**
4. **审查文件权限** — 考虑 0o640
5. **GraphRunner 级别添加超时机制**
6. **LLM 调用前添加工具可用性验证**

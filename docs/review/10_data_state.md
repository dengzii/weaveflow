# 10. 数据与状态审查

> 评级：**良好**

## 状态来源唯一性 — 优秀

### 集中式状态架构

```go
// runtime/state.go:35
type State map[string]any  // 根级别业务数据

// 保留命名空间（runtime/state.go:12-14）:
__wf_conversation  // 对话/LLM 状态
__wf_scopes        // 执行作用域
```

### 状态组织

| 层级 | 职责 | 管理方 |
|------|------|--------|
| 根级别键 | 用户业务数据 | 节点直接写入 |
| `__wf_conversation` | 对话消息/迭代/最终答案 | ConversationExtension |
| `__wf_scopes` | 按阶段隔离的状态 | 作用域访问器 |

**Conversation 是门面接口** (`conversation_state.go:23-25`), 非独立副本——通过 `conversationSource()` 访问 `__wf_conversation` 命名空间。

**克隆语义明确**: `CloneState()` 控制拷贝内容（过滤基础设施键），确保单一来源。

## 重复状态分析 — 无冗余

### RunRecord vs State — 无重叠

```go
// runtime/runner_types.go:75-91
type RunRecord struct {
    RunID, Status, CurrentNodeID, LastStepID  // 仅运行时控制元数据
    Timestamps, ErrorFields                   // 不包含业务状态
}
```

### StepRecord vs State — 无重叠

```go
// runtime/runner_types.go:93-107
type StepRecord struct {
    StepID, NodeID, Status                    // 仅步骤级控制信息
    CheckpointIDs, Timestamps                 // 不复制业务状态
}
```

### StateSnapshot 分区清晰

```go
// runtime/state_snapshot.go:15-23
type StateSnapshot struct {
    Version      string                 // 版本标识
    Runtime      RuntimeState           // 控制元数据
    Conversation *ConversationState     // 对话数据
    Shared       GraphState             // 业务根级数据
    Scopes       map[string]GraphState  // 作用域数据
    Internal     map[string]GraphState  // 内部命名空间
    Artifacts    []ArtifactRef          // 引用指针
}
```

**各字段无数据重叠。**

## 本地缓存失效 — 无缓存层

### 文件存储直接 I/O

| 存储 | 实现 | 缓存 |
|------|------|------|
| FileExecutionStore | 直接文件 I/O + Mutex | 无 |
| FileCheckpointStore | 直接文件 I/O + Mutex | 无 |
| FileEventSink | JSONL 追加 + Mutex | 无 |
| FileArtifactStore | 直接文件 I/O + Mutex | 无 |

每次读取都是新的文件 I/O 操作，无 TTL 缓存。

### 内存仓库

- `in_memory_repository.go` — 用 `RWMutex` 保护，`Load()` 返回克隆
- `file_repository.go` — 原子写入（temp dir + rename），无缓存

## 数据结构稳定性 — 中等

### 版本策略

```go
// state_snapshot.go:9-10
const (
    CommonStateSchemaID = "weaveflow.state.v2"
    DefaultStateVersion = CommonStateSchemaID
)
```

- 单一活跃版本: `weaveflow.state.v2`
- 每个快照存储版本字段
- Codec 构造函数接受版本参数

### 遗留版本处理

```go
// snapshot_codec.go:48-50
if looksLikeLegacyScopeLayout(snapshot.Version, snapshot.Scopes) {
    return StateSnapshot{}, fmt.Errorf("legacy state snapshot layout is no longer supported")
}
```

**v1 快照被硬拒绝，无迁移路径。**

### DSL 版本

```go
// dsl/dsl.go:28, 52-53
GraphDefinitionVersion = "1.0"
type GraphDefinition struct {
    Version string  // 默认 "1.0"
}
```

### 演进能力

| 操作 | 可行性 |
|------|--------|
| 添加新字段 | ✅ JSON `additionalProperties: true` |
| 删除/重命名字段 | ❌ 破坏旧数据 |
| v1 快照兼容 | ❌ 硬拒绝 |
| 前向兼容 | ❌ 新代码遇到旧格式可能失败 |

## 数据迁移机制 — 缺失

**无迁移框架**：
- 无迁移运行器
- 无 schema 迁移脚本
- 无版本到版本转换函数

存在但非迁移用途的机制：
- `NormalizeInputState()` — 规范化用户输入，非迁移
- `MergeInputState()` — 合并基础状态与恢复输入，非迁移

## 数据库 Schema 版本管理

### 检查点记录版本化

```go
// runner_types.go:109-119
type CheckpointRecord struct {
    StateCodec   string  // codec 名称 ("json")
    StateVersion string  // e.g., "weaveflow.state.v2"
    PayloadRef   string  // 路径引用
}
```

- 元数据与载荷分离存储
- 允许运行时选择解码器

### 事件格式

- JSONL 格式（每行一个事件）
- `Payload` 为 `json.RawMessage`（版本无关）
- 无事件 schema 版本化

### 缺失

- Run/Step 记录结构无版本化
- 无版本特定反序列化逻辑
- 仅遗留检测，无遗留迁移

## 配置向后兼容 — 薄弱

### 图定义

- `Version` 字段存储（默认 "1.0"）
- 验证忽略版本字段做兼容性检查
- 无不同图版本的特殊处理

### 节点配置

- `Config map[string]any` — 无版本化
- 旧配置多余字段不报错（JSON 反序列化特性）

### 兼容性评估

| 场景 | 可行性 |
|------|--------|
| 旧配置加载 | 部分可行（多余字段容忍） |
| 字段类型变更 | ❌ 破坏 |
| 节点/条件类型移除 | ❌ 图无法编译 |

## 用户数据备份/恢复 — 可恢复

### 数据存储结构

```
{dataDir}/
├── runs/{runID}.json           # Run 记录
├── steps/{runID}/{stepID}.json # Step 记录
├── checkpoints/{runID}/
│   ├── {checkpointID}.json     # 检查点元数据
│   └── payloads/{checkpointID}.bin  # 状态载荷
├── artifacts/{runID}/
│   ├── {artifactID}.json       # 制品元数据
│   └── payloads/{artifactID}.bin    # 制品数据
├── events/{runID}.jsonl        # 事件日志
└── memory/{YYYY-MM-DD}.json    # 记忆条目
```

### 恢复能力

- ✅ 单个文件可恢复
- ✅ 任意检查点可恢复状态
- ✅ 记忆按日期组织，可重建时间线
- ✅ 原子写入（temp + rename）减少损坏

### 恢复限制

- 无事务支持——部分写入可能导致不一致
- 无校验和/哈希验证
- 目录遍历错误静默跳过损坏文件

## 并发读写安全

### 文件存储锁保护

| 存储 | 锁类型 | 写安全 | 读安全 |
|------|--------|--------|--------|
| FileExecutionStore | `sync.Mutex` | ✅ | ⚠️ GetRun 未加锁 |
| FileCheckpointStore | `sync.Mutex` | ✅ | ✅ |
| FileEventSink | `sync.Mutex` | ✅ | N/A |
| FileArtifactStore | `sync.Mutex` | ✅ | ❌ **Load/List 未加锁** |
| inMemoryRepository | `sync.RWMutex` | ✅ | ✅ 克隆返回 |
| fileRepository | **无锁** | ❌ | ❌ |

### 问题

1. **Artifact 读未加锁** (`artifact_store.go:69-94`) — 可能读到部分写入的元数据
2. **Memory fileRepository Store 无锁** (`file_repository.go:43-96`) — 并发 Store 可能导致数据丢失
3. **writeRunnerBinaryFile 在 mutex 外** (`runner_store.go:419-440`) — temp write + rename 之间有竞态窗口
4. **跨进程不安全** — 两个 FileArtifactStore 实例（不同进程）可能损坏共享文件

### 原子写入模式

```go
// runner_store.go:419-440 — 正确的 write-then-rename
temp := os.CreateTemp(...)
temp.Write(data)
temp.Close()
os.Rename(tempPath, path)  // 原子替换
```

## 关键问题汇总

| 问题 | 严重度 | 影响 |
|------|--------|------|
| v1 快照被拒绝，无迁移 | 高 | 升级时数据丢失 |
| Artifact 读未加锁 | 中 | 可能读到损坏数据 |
| Memory fileRepository 无线程安全 | 中 | 并发 Store 数据丢失 |
| 无 schema 迁移框架 | 中 | 升级路径不明 |
| 无数据完整性校验 | 低 | 静默损坏未检测 |
| 目录遍历静默跳过错误 | 低 | 损坏文件被忽略 |

## 设计优势

1. **清晰的状态层级** — 命名空间架构防止键冲突
2. **显式克隆** — 写时复制语义防止数据共享 bug
3. **版本化快照** — 可检测旧格式
4. **原子文件写入** — temp + rename 减少损坏
5. **职责分离** — 元数据记录不复制业务状态
6. **扩展系统** — 状态扩展允许演进对话字段

## 建议

### 高优先级
1. 实现状态迁移框架（v1 → v2 转换器）
2. Artifact 读操作加锁
3. Memory fileRepository 添加线程安全

### 中优先级
4. 添加完整性校验（CRC/哈希）
5. 文档化升级路径
6. 多文件操作添加事务日志
7. write-then-rename 纳入 mutex 保护

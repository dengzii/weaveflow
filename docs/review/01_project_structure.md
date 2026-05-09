# 1. 项目结构审查

> 评级：**良好**

## 项目概览

- **模块名**: `weaveflow`
- **Go 版本**: 1.26.1
- **项目类型**: 本地 LLM/Agent Graph 运行时和调试框架
- **总 Go 文件**: 126（不含测试）
- **总代码量**: ~19,500 LOC（不含测试和依赖）

## 目录结构

```
weaveflow/
├── cmd/                          # 二进制入口
│   ├── neo/main.go              # Neo agent 服务器 (端口 9090, OpenAI)
│   └── service/main.go          # 通用服务
├── dsl/                          # 领域特定语言 (708 LOC)
│   ├── dsl.go                   # DSL 核心定义
│   ├── dsl_runtime.go
│   ├── node_spec.go
│   ├── registry_schema.go
│   ├── state_contract.go
│   └── state_contract_test.go
├── nodes/                        # 29 个内置图节点实现 (7,313 LOC) — 最大包
│   ├── node.go                  # Node 接口与基础类型
│   ├── llm_node.go              # LLM 推理节点
│   ├── planner.go               # 规划与分解
│   ├── orchestration_router.go  # 路由逻辑
│   ├── verifier.go              # 输出验证
│   ├── context_assembler.go     # 上下文组装
│   ├── context_reducer.go       # 上下文精简
│   ├── iterator.go              # 循环控制
│   ├── memory_recall.go         # 记忆检索
│   ├── memory_write.go          # 记忆写入
│   ├── intent_analyzer.go       # 意图分析
│   ├── session_bootstrap.go     # 会话初始化
│   ├── human_message.go         # 人类交互节点
│   ├── tool_call_node.go        # 工具调用
│   ├── tool_policy_guard.go     # 工具安全门
│   ├── cost_budget_guard.go     # Token 预算控制
│   ├── approval_gate.go         # 用户审批流
│   ├── finalizer.go             # 输出终态化
│   ├── observation_recorder.go  # 事件记录
│   ├── replanner.go             # 重规划
│   ├── subgraph.go              # 嵌套图执行
│   ├── plan_step_executor.go    # 计划执行
│   ├── llama_cpp_node.go        # 本地模型集成
│   ├── usage.go                 # Token 用量追踪
│   └── *_test.go (5 个测试文件)
├── runtime/                      # 图执行运行时 (4,677 LOC)
│   ├── graph_runner.go          # 主执行引擎 (869 LOC)
│   ├── graph_runtime.go         # 运行时生命周期
│   ├── state.go                 # 状态管理 (270 LOC)
│   ├── state_extension.go       # 状态扩展
│   ├── state_fields.go          # 状态字段定义
│   ├── state_merge.go           # 状态合并
│   ├── state_snapshot.go        # 状态快照
│   ├── snapshot_codec.go        # 序列化 (15K)
│   ├── redaction.go             # 敏感数据脱敏 (1015 LOC)
│   ├── runner_store.go          # 持久化层 (11K)
│   ├── artifact_store.go        # 制品管理
│   ├── runner_types.go          # 类型定义
│   ├── contract_validation.go   # 运行时契约校验
│   ├── logging.go               # 日志工具
│   └── *_test.go (5 个测试文件)
├── memory/                       # 长期记忆系统 (949 LOC)
│   ├── memory.go
│   ├── manager.go               # 记忆管理接口
│   ├── bm25.go                  # BM25 排序
│   ├── in_memory_repository.go  # 内存存储
│   ├── file_repository.go       # 文件存储
│   ├── tokenizer_fallback.go    # 回退分词器
│   └── tokenizer_gojieba_cgo.go # CGO 中文分词
├── tools/                        # 内置工具实现 (916 LOC)
│   ├── tool.go                  # 工具接口 (626 LOC)
│   ├── file.go                  # 文件操作 (7K)
│   ├── file_read.go / file_write.go
│   ├── web_fetch.go / web_search.go
│   ├── calculator.go
│   ├── current_time.go
│   ├── ask_user_question.go     # 空桩
│   ├── bash.go                  # 空桩
│   └── file_test.go
├── llama_cpp/                    # 本地 LLM 推理集成 (1,470 LOC)
│   ├── llama_cpp.go / model.go
│   ├── bin/ / include/ / include-min/
├── internal/                     # 内部包
│   ├── neo/                     # Neo 服务实现 (877 LOC)
│   ├── server/                  # HTTP API 服务 (1,354 LOC)
│   └── redact/                  # 敏感数据脱敏 (145 LOC)
├── examples/                     # 示例
│   ├── graph/ / node/ / llama_cpp/
├── debug/replay/                 # Web 重放调试器
├── docs/                         # 文档（原为空）
├── 根级别文件 (23 个 Go 文件, 6,036 LOC)
│   ├── registry.go              # 主注册表 (783 LOC)
│   ├── graph.go                 # 图构建器 (612 LOC)
│   ├── conditions.go            # 边条件 (410 LOC)
│   └── *_module.go (10 个模块)
├── go.mod / go.sum
└── README.md
```

## 模块边界与分层

```
┌─────────────────────────────────────────────────────┐
│ 示例与二进制 (cmd/, examples/, debug/)               │
├─────────────────────────────────────────────────────┤
│ 主包: weaveflow (根级 *.go 文件)                     │
│  - Graph, Registry, Conditions, Modules             │
├─────────────────────────────────────────────────────┤
│ 功能包                                               │
│  - nodes/ (29 个节点类型)                            │
│  - tools/ (10 个工具类型)                            │
│  - memory/ (记忆子系统)                              │
│  - llama_cpp/ (本地 LLM)                            │
├─────────────────────────────────────────────────────┤
│ 运行时与基础设施                                      │
│  - runtime/ (执行引擎)                               │
│  - dsl/ (序列化)                                    │
│  - internal/ (服务器, neo, 脱敏)                     │
├─────────────────────────────────────────────────────┤
│ 外部依赖                                             │
│  - langchaingo, langgraphgo, openai, gin            │
└─────────────────────────────────────────────────────┘
```

## 依赖关系（无循环依赖）

```
weaveflow.root
  ├─→ dsl/ (无内部依赖)
  ├─→ nodes/ (依赖: dsl, runtime, memory, tools, llama_cpp)
  ├─→ runtime/ (依赖: internal/redact)
  ├─→ memory/ (无 weaveflow 依赖)
  ├─→ tools/ (无 weaveflow 依赖)
  ├─→ llama_cpp/ (无 weaveflow 依赖)
  └─→ internal/
        ├─→ neo/ (依赖: weaveflow, memory, nodes, tools)
        ├─→ server/ (依赖: weaveflow, llama_cpp, neo, internal/redact)
        └─→ redact/ (无依赖)
```

## "万能包" 分析

| 包 | LOC | 风险 | 分析 |
|----|-----|------|------|
| nodes/ | 7,313 | 中 | 29 个不同职责的节点实现混于一包 |
| runtime/graph_runner.go | 869 | 高 | 处理复杂状态转换的大文件 |
| registry.go | 783 | 中 | 主注册表，编排节点类型和条件 |

**建议**：将 `nodes/` 拆分为语义子包：
- `nodes/core/` — 基础节点
- `nodes/planner/` — 规划节点
- `nodes/context/` — 上下文节点
- `nodes/safety/` — 安全节点
- `nodes/io/` — I/O 节点
- `nodes/execution/` — 执行节点

## 废弃/冗余代码

| 文件 | 问题 | 建议 |
|------|------|------|
| `tools/bash.go` | 空文件（2行） | 删除或实现 |
| `tools/ask_user_question.go` | 空文件（2行） | 删除或实现 |
| `examples/node/main.go` | 12 个示例函数全部注释 | 文档说明或移除 |

## 测试覆盖

| 包 | 测试文件 | 覆盖率 |
|----|---------|--------|
| dsl/ | 1 | ~5% |
| nodes/ | 5 | ~17% |
| runtime/ | 5 | ~17% |
| memory/ | 0 | **0%（关键缺口）** |
| tools/ | 1 | ~10% |
| root | 3 | ~13% |

## Go 惯例遵循

- ✅ `cmd/` 存放可执行文件
- ✅ `internal/` 存放非导出包
- ✅ 测试文件与源码同目录
- ✅ 扁平包结构，无过度嵌套
- ⚠️ 模块名 `weaveflow` 与仓库名 `falcon` 不一致
- ⚠️ `.idea/` 目录应加入 `.gitignore`

## 总结

| 类别 | 状态 |
|------|------|
| 目录结构 | 良好 — 遵循 Go 惯例 |
| 模块边界 | 良好 — 无循环依赖 |
| 代码组织 | 中等 — nodes/ 包过大 |
| 废弃代码 | 存在 — 2 个空桩文件，12 个注释示例 |
| 测试覆盖 | 薄弱 — memory/ 零测试 |
| 依赖 | 良好 — 精简、无冗余 |

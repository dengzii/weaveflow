# 8. 依赖审查

> 评级：**7.6/10**

## 项目概要

- **Go 版本**: 1.26.1
- **模块名**: `weaveflow`
- **直接依赖**: 6 个
- **总依赖链**: 341 个模块

## 直接依赖分析

### 1. github.com/gin-gonic/gin v1.12.0 — Web 框架

| 维度 | 评估 |
|------|------|
| 维护状态 | ✅ 活跃维护 |
| 声誉 | 行业标准 Go HTTP 框架 |
| 安全漏洞 | 无已知严重漏洞 |
| 版本锁定 | ✅ v1.12.0 |
| 使用范围 | 102 处引用 |
| 平台兼容 | ✅ 纯 Go |

### 2. go.uber.org/zap v1.27.1 — 结构化日志

| 维度 | 评估 |
|------|------|
| 维护状态 | ✅ 活跃维护 |
| 声誉 | Uber 出品，行业标准 |
| 安全漏洞 | 无 |
| 版本锁定 | ✅ v1.27.1 |
| 使用范围 | 152 处引用 |
| 平台兼容 | ✅ 纯 Go |

### 3. github.com/google/uuid v1.6.0 — UUID 生成

| 维度 | 评估 |
|------|------|
| 维护状态 | ✅ 活跃维护 |
| 声誉 | Google 官方库 |
| 安全漏洞 | 无 |
| 版本锁定 | ✅ v1.6.0 |
| 使用范围 | 50 处引用 |
| 平台兼容 | ✅ 纯 Go |

### 4. github.com/smallnest/langgraphgo v0.8.5 — 图运行时

| 维度 | 评估 |
|------|------|
| 维护状态 | ⚠️ 较新库，社区较小 |
| 声誉 | LangChain 生态 Go 实现 |
| 安全漏洞 | 无已知 |
| 版本锁定 | ✅ v0.8.5 |
| 使用范围 | 11 处引用 |
| 平台兼容 | ✅ 纯 Go |
| **风险** | 社区较小，采用率低于 Python 版 |

### 5. github.com/tmc/langchaingo v0.1.14 — LLM 集成框架

| 维度 | 评估 |
|------|------|
| 维护状态 | ⚠️ 社区维护，更新较慢 |
| 声誉 | 社区 LangChain Go 实现 |
| 安全漏洞 | 无已知 |
| 版本锁定 | ✅ v0.1.14 |
| 使用范围 | 67 处引用 |
| **风险** | **pre-1.0 版本，API 可能不稳定** |
| 平台兼容 | ✅ 纯 Go |

### 6. github.com/yanyiwu/gojieba v1.4.7 — 中文分词 (CGO)

| 维度 | 评估 |
|------|------|
| 维护状态 | ⚠️ 维护关注度低 |
| 声誉 | Jieba 中文 NLP 分词器 |
| 安全漏洞 | CGO 引入 C 内存安全风险 |
| 版本锁定 | ✅ v1.4.7 |
| 使用范围 | 仅 3 处引用（build-tag 隔离） |
| **平台风险** | **需要 CGO + C 编译器 + DLL** |

## CGO / Windows 部署问题

### 严重问题

gojieba CGO 要求：
- 构建需要 `CGO_ENABLED=1`
- Windows 需要 C 编译器（MSVC 或 GCC）
- 运行时依赖 `libllama.dll`, `ggml.dll`, `ggml-base.dll`, `ggml-cpu.dll`
- 无法交叉编译

### 缓解方案

```go
//go:build cgo && windows  // gojieba 激活
//go:build !cgo            // 回退 Unicode 分词器
```

回退方案存在但有功能损失（Unicode 分词 vs Jieba 分词）。

## 间接依赖关注点

| 依赖 | 类型 | 状态 | 关注 |
|------|------|------|------|
| `bytedance/sonic` | JSON 编码 | ✅ | Gin 隐式使用 |
| `sashabaranov/go-openai` | OpenAI 客户端 | ✅ | 通过 langchaingo 引入 |
| `go.mongodb.org/mongo-driver/v2` | 数据库驱动 | ⚠️ | **代码中零引用，未使用** |
| `google.golang.org/protobuf` | 序列化 | ✅ | 标准库 |
| `golang.org/x/crypto` | 加密 | ✅ | 标准扩展 |
| `pkoukk/tiktoken-go` | Token 计数 | ✅ | OpenAI token 限制 |

## 重复功能检查

**无显著重复**：
- 仅一个 HTTP 框架（Gin）
- 仅一个日志框架（Zap）
- 仅一个 UUID 库
- LangChain 和 LangGraph 服务不同目的

## 重型依赖用于小功能

| 依赖 | 大小 | 使用情况 | 建议 |
|------|------|---------|------|
| MongoDB Driver v2.5.0 | ~3MB+ | **零引用** | `go mod tidy` 清理 |
| QUIC 相关库 | 较大 | Gin/net 间接引入 | 可接受 |

## 许可证审查

| 依赖 | 许可证 | 兼容性 |
|------|--------|--------|
| 项目本身 | MIT | ✅ |
| Gin | MIT | ✅ |
| Zap | MIT | ✅ |
| UUID | BSD | ✅ |
| LangChain Go | MIT | ✅ |
| Gojieba | MIT | ✅ |

**无许可证冲突或限制。**

## 风险评估

### 严重风险
- **CGO/Windows DLL 依赖** — 部署阻碍（gojieba）

### 中等风险
- **Pre-1.0 API 稳定性** — langchaingo v0.1.14 可能在小版本更新时破坏
- **未使用 MongoDB Driver** — 3MB+ 冗余

### 低风险
- Gin, Zap, UUID — 高维护度
- 无严重 CVE
- 无许可证冲突

## 依赖健康评分

| 指标 | 分数 | 说明 |
|------|------|------|
| 维护度 | 8/10 | 主要依赖好，pre-1.0 有风险 |
| 安全性 | 8/10 | 无已知 CVE，CGO 增加风险 |
| 兼容性 | 6/10 | Windows CGO 限制显著 |
| 冗余度 | 7/10 | 341 模块合理，存在未使用依赖 |
| 许可证 | 10/10 | 全部兼容 |
| **总分** | **7.6/10** | |

## 建议

### 立即
1. 运行 `go mod tidy` 清理未使用依赖
2. 文档化 CGO/Windows 构建要求
3. 考虑锁定 langchaingo 到具体 commit

### 中期
4. 监控 langchaingo pre-1.0 → 1.0 迁移
5. 评估用直接 API 调用替代 langchaingo（如使用场景简单）
6. 非中文部署禁用 Jieba（简化构建）

### 长期
7. 规划 langchaingo 维护终止时的迁移路径
8. 关注 langgraphgo 稳定性改进

# 快速开始

## 环境要求

- Go 1.26 或更高版本
- Git
- 运行模型调用示例时，需要一个兼容 OpenAI 的服务端点

## 安装模块

```bash
go get github.com/dengzii/weaveflow
```

如需直接使用仓库源码：

```bash
git clone https://github.com/dengzii/weaveflow.git
cd weaveflow
go build ./...
```

## 配置模型

默认图示例从环境变量读取兼容 OpenAI 的模型配置：

```bash
export OPENAI_API_KEY=<your-api-key>
export OPENAI_BASE_URL=<your-base-url>
export OPENAI_MODEL=<your-model>
```

PowerShell：

```powershell
$env:OPENAI_API_KEY = "<your-api-key>"
$env:OPENAI_BASE_URL = "<your-base-url>"
$env:OPENAI_MODEL = "<your-model>"
```

## 运行图示例

```bash
go run ./examples/graph
```

该示例会构建一个 ReAct 风格的图，持久化图定义，在 `.local/instance/` 下记录执行数据，并演示从检查点恢复。

## 运行无需模型的示例

无需凭据即可通过以下示例了解运行时行为：

```bash
go run ./examples/graph/conditional_routing.go
go run ./examples/graph/failure_fallback.go
go run ./examples/graph/human_approval.go
```

接下来可以阅读[图定义](/zh/concepts/graph-definition)和[状态绑定](/zh/concepts/state-bindings)。

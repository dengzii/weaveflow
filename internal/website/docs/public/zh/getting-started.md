# 快速开始

本指南从克隆仓库开始，带你完成一次可检查的 Run。先运行无需模型的示例，熟悉运行时；准备好后，再配置模型驱动的智能体工作流。

## 环境要求

- Go 1.26 或更高版本
- Git
- 运行模型调用示例时，需要一个兼容 OpenAI 的服务端点

先检查工具链：

```bash
go version
git --version
```

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

## 运行无需模型的示例

无需凭据即可逐个运行这些无模型示例：

```bash
go run ./examples/graph/conditional_routing.go
go run ./examples/graph/dynamic_map_reduce.go
go run ./examples/graph/failure_fallback.go
go run ./examples/graph/human_approval.go
go run ./examples/graph/fan_in_fan_out.go
```

这些程序会构建图、使用本地状态执行并输出结果。结合[运行时模型](/zh/concepts/runtime)阅读输出，了解其中的 Run、Step、Event
和 Checkpoint。

## 配置模型

默认图示例会从环境变量读取 OpenAI 兼容模型配置：

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.example.com/v1"
export OPENAI_MODEL="your-model"
```

本地兼容服务的地址应指向 API 根路径，例如 `http://localhost:8000/v1`。不要将凭据写入 Graph Definition 或提交
包含密钥的 shell 文件。

PowerShell：

```powershell
$env:OPENAI_API_KEY = "your-api-key"
$env:OPENAI_BASE_URL = "https://api.example.com/v1"
$env:OPENAI_MODEL = "your-model"
```

## 运行图示例

```bash
go run ./examples/graph
```

该示例构建 ReAct 风格的图，保存图定义，在 `.local/instance/` 下记录运行数据，并演示如何从检查点恢复。

首次运行会创建本地 Run；随后示例会读取已保存的 Graph Definition，找到可以继续执行的 Run，并用新输入恢复。删除
`.local/instance/` 可以重新开始。

## 启动调试服务器

```bash
go run ./cmd/server -addr 127.0.0.1:8080 -data .local/server \
  -graph examples/codespaces_demo/graph.json
```

确认服务已就绪：

```bash
curl http://127.0.0.1:8080/healthz
```

服务器提供 Workbench 所需的 Graph、Session、Run 和注册表 API。接着阅读 [Workbench](/zh/guides/workbench)。

## 下一步

1. 阅读[图定义](/zh/concepts/graph-definition)了解版本、节点、边和策略。
2. 在修改节点或增加并行分支前阅读[状态绑定](/zh/concepts/state-bindings)。
3. 用[可运行示例](/zh/guides/examples)把概念对应到源码文件。
4. 准备模型调用时阅读[模型提供商](/zh/guides/model-providers)。
5. 按照[部署服务器](/zh/deployment)中的说明运行打包镜像。

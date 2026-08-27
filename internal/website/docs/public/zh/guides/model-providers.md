# 配置模型提供商

WeaveFlow 通过 OpenAI 兼容客户端调用模型，支持 OpenAI、Azure、DeepSeek、Gemini、vLLM、Mistral、xAI 和 OpenRouter，
并支持 Chat Completions 与 Responses 两种请求格式。

## 示例环境变量

```bash
export OPENAI_API_KEY="your-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
export OPENAI_MODEL="your-model"
```

使用默认 OpenAI 服务时可以省略 `OPENAI_BASE_URL`。请将凭据放在本地 `.env` 或密钥管理器中；不要提交凭据、把它们写入 Graph
Definition，也不要打印到日志。

## 在 Go 中选择提供商

```go
model, err := openai.New(
    openai.WithToken(os.Getenv("OPENAI_API_KEY")),
    openai.WithModel(os.Getenv("OPENAI_MODEL")),
    openai.WithBaseURL(os.Getenv("OPENAI_BASE_URL")),
    openai.WithProvider(openai.ProviderDeepSeek),
    openai.WithAPIFormat(openai.APIFormatChatCompletions),
)
if err != nil {
    log.Fatal(err)
}
```

客户端默认使用 Chat Completions；如果服务端实现的是 Responses API，应选择 `openai.APIFormatResponses`。提供商和请求格式
必须与实际端点匹配。

## 配置服务器端 Assistant

同时配置以下变量后，`cmd/server` 会启用可选的 Assistant API：

```bash
export WEAVEFLOW_ASSISTANT_API_KEY="your-key"
export WEAVEFLOW_ASSISTANT_MODEL="your-model"
export WEAVEFLOW_ASSISTANT_BASE_URL="https://api.openai.com/v1"
export WEAVEFLOW_ASSISTANT_PROVIDER="openai"
export WEAVEFLOW_ASSISTANT_API_FORMAT="responses"
go run ./cmd/server -data .local/server
```

Assistant 配置与 Graph Definition 中引用的模型 ID 相互独立。节点可以通过 `model_id` 从运行时模型上下文中选择模型；
如果需要多个模型，请使用 Workbench 设置或自行集成服务器。

## 兼容性检查

1. 确认 Base URL 是 API 根地址，通常以 `/v1` 结尾。
2. 单独验证模型 ID 是否有效。
3. 选择服务端支持的请求格式。
4. 先运行小型 `text_generation` 或 `llm_turn` 图。
5. 请求失败时检查 Run Events 和提供商错误详情。

如果还没有凭据，请先运行[无模型示例](/zh/guides/examples#无需模型的运行时控制)。

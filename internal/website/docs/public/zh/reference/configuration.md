# 配置参考

WeaveFlow 的配置分为命令行参数、环境变量和 Graph/Session 设置三层。凭据应放在环境变量或托管密钥引用中，Graph Definition
只描述行为，不保存密钥。

## 服务器参数

| 参数 | 默认值 | 用途 |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | 监听地址；非回环地址需要管理令牌。 |
| `-data` | `.local/wf` | 运行时数据目录。 |
| `-secret-dir` | 空 | 文件型密钥引用目录。 |
| `-prefix` | 空 | HTTP 路由前缀，如 `/debug`。 |
| `-cors-origins` | `*` | 逗号分隔的浏览器来源，或 `*`。 |
| `-graph` | 空 | 启动时预加载的 Graph JSON。 |
| `-log-level` | `debug` | `debug`、`info` 或 `error`。 |

默认的 `-cors-origins=*` 允许任意浏览器来源，仅适合本地开发。跨来源生产部署时，请改用明确的来源白名单。

## 模型变量

| 变量 | 用途 |
| --- | --- |
| `OPENAI_API_KEY` | 供示例使用的默认 OpenAI 兼容凭据。 |
| `OPENAI_BASE_URL` | API 根地址，通常以 `/v1` 结尾。 |
| `OPENAI_MODEL` | 默认模型 ID。 |
| `WEAVEFLOW_ASSISTANT_API_KEY` | 与 Assistant 模型 ID 同时设置时，启用服务器端 Assistant。 |
| `WEAVEFLOW_ASSISTANT_MODEL` | Assistant 模型 ID。 |
| `WEAVEFLOW_ASSISTANT_BASE_URL` | Assistant 提供商地址。 |
| `WEAVEFLOW_ASSISTANT_PROVIDER` | Assistant 提供商配置。 |
| `WEAVEFLOW_ASSISTANT_API_FORMAT` | `chat_completions` 或 `responses`。 |

## 安全与运行时变量

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `WEAVEFLOW_MANAGEMENT_TOKEN` | 空 | 保护管理路由的 Bearer token。 |
| `WEAVEFLOW_TOOL_WORKDIR` | 未设置 | 文件工具工作区；未设置时使用进程工作目录。 |
| `WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK` | `false` | 绕过工作区检查；生产环境不建议使用。 |
| `WEAVEFLOW_BASH_TIMEOUT` | `120000` | Bash 工具超时（毫秒）。 |
| `WEAVEFLOW_BASH_ALLOWLIST` | 空 | 可选的命令白名单。 |
| `WEAVEFLOW_CODEX_WORKSPACE_ROOTS` | 空 | Codex 允许运行的根目录。 |
| `WEAVEFLOW_CLAUDE_WORKSPACE_ROOTS` | 空 | Claude 允许运行的根目录。 |

## Graph 与 Session 设置

Graph Definition 设置拓扑、模块、节点/条件配置、策略和元数据；Graph Session 是捕获定义修订和运行时设置的不可变执行视图。
创建 Session 前应根据当前注册表验证定义。

## 容器变量

容器额外变量和默认值参见[部署服务器](/zh/deployment)及仓库中的 `scripts/.env.example`。

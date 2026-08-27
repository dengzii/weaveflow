# HTTP API 参考

调试服务器提供 Graph Definition、不可变 Session、Run、运行检查、注册表、内存、聊天频道和 Trigger 的 JSON API。
下列路径均相对于可选的 `-prefix`。

## 认证

`GET /healthz` 和公开的 Trigger 调用接口不需要管理认证。Graph、Run、注册表、内存、Assistant 与 Trigger 管理路由需要：

```http
Authorization: Bearer <WEAVEFLOW_MANAGEMENT_TOKEN>
```

发送令牌时使用 HTTPS 或私有网络；未设置令牌时，非回环服务器不会启动。

## Graph 与 Session

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/graphs` | 列出 Graph 摘要。 |
| `GET` | `/graphs/:graph_id` | 获取 Graph 详情和注册表元数据。 |
| `DELETE` | `/graphs/:graph_id` | 删除 Graph 及其保留的运行数据。 |
| `GET` | `/graphs/:graph_id/retention-audit` | 在删除或清理前查看保留数据。 |
| `POST` | `/graphs/:graph_id/sessions` | 创建不可变 Graph Session。 |
| `GET` | `/graphs/:graph_id/sessions/:session_id` | 获取 Session 详情。 |
| `POST` | `/graphs/:graph_id/analysis/initial-state-requirements` | 分析初始 State 要求。 |

定义会在创建 Session 前完成验证。请保存返回的 Session ID，后续运行都使用它。

## 运行时与注册表

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/registry` | 发现节点、条件、模块、能力和 reducer 契约。 |
| `GET` | `/runtime/tools` | 列出运行时上下文中已安装的工具。 |

## Run 与证据

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/graphs/:graph_id/sessions/:session_id/runs` | 启动 Run。 |
| `GET` | `/graphs/:graph_id/runs` | 列出 Graph 的 Run。 |
| `GET` | `/graphs/:graph_id/runs/:run_id/inspection` | 获取摘要、Steps、State 和诊断。 |
| `GET` | `/graphs/:graph_id/runs/:run_id/events` | 分页获取 Events。 |
| `GET` | `/graphs/:graph_id/events/stream` | 以 SSE 推送 Graph 级 Events。 |
| `GET` | `/graphs/:graph_id/runs/:run_id/checkpoints/:checkpoint_id` | 获取 Checkpoint。 |
| `GET` | `/graphs/:graph_id/runs/:run_id/artifacts` | 列出 Artifacts。 |
| `GET` | `/graphs/:graph_id/runs/:run_id/artifacts/:artifact_id` | 获取单个 Artifact。 |
| `POST` | `/graphs/:graph_id/runs/:run_id/pause` | 请求安全暂停。 |
| `POST` | `/graphs/:graph_id/runs/:run_id/resume` | 从兼容检查点恢复。 |
| `POST` | `/graphs/:graph_id/runs/:run_id/steps/:step_id/effect-resolution` | 记录未确定副作用的结果。 |
| `POST` | `/graphs/:graph_id/runs/:run_id/cancel` | 取消 Run。 |
| `DELETE` | `/graphs/:graph_id/runs/:run_id` | 删除 Run 及其保留证据。 |
| `POST` | `/graphs/:graph_id/runs/:run_id/forks` | 从保留证据创建 Run 分支。 |
| `GET` | `/graphs/:graph_id/runs/:run_id/compare/:other_run_id` | 比较两个 Run。 |

## 内存、Assistant 与聊天

内存管理使用 `/memory/:namespace` 提供列出、搜索、读取、写入和删除操作。可选 Assistant 服务使用
`/assistant/status`、`/assistant/sessions/:session_id`、`/assistant/sessions/:session_id/messages`、
`/assistant/jobs/:job_id` 和 `/assistant/jobs/:job_id/stream`。聊天频道设置使用：

```text
/chat-channels/:channel_id/setup-sessions
/chat-channels/:channel_id/setup-sessions/:session_id/verification
```

## Trigger 管理

Trigger 管理位于 `/graphs/:graph_id/triggers`（`GET` 列出，`PUT` 替换）；公开调用提供三种 POST 路径：`invocations`、
`webhook` 和 `chat`。

## 健康检查与错误

```bash
curl -i http://127.0.0.1:8080/healthz
```

`/healthz` 返回服务器状态、版本和 UTC 构建时间。请求失败时，请保留 JSON 响应和关联的 Event ID，方便排障。

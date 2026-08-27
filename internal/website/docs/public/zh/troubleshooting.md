# 故障排查

## 服务器拒绝监听非回环地址

这是有意的安全保护。使用 `-addr 0.0.0.0:8080` 前设置强 `WEAVEFLOW_MANAGEMENT_TOKEN`；开发时则绑定回环地址。

## 模型请求返回 404 或不支持的操作

先检查 API 根地址和请求格式：

```bash
curl "$OPENAI_BASE_URL/models" -H "Authorization: Bearer $OPENAI_API_KEY"
```

许多兼容服务需要 `/v1`。确保 `chat_completions` 或 `responses` 与提供商匹配，并单独确认模型 ID。

## Graph Definition 验证失败

获取 `GET /registry`，对照当前节点、条件、State Module、能力和 reducer 契约。常见原因包括类型拼写错误、缺少必需端口、
将路径写入 `config`、未知模块版本，或并行写入没有 reducer。

## Run 暂停后无法恢复

检查 Run 状态、最后一个 Checkpoint 和待处理 Step。补充需要的输入或解决待确认副作用；在确认原 Run 的副作用状态前，不要
创建第二个 Run，并始终针对同一个 Graph Session 修订恢复。

## 找不到工具

调用 `GET /runtime/tools`，将返回的工具 ID 与节点的 `tool_ids` 对照。服务器必须先把工具安装到运行时上下文；文件工具
还需要一个允许访问的工作区根目录。

## Workbench 无法连接 API

检查 API 来源、路由前缀和 CORS 白名单。带前缀时，将 Backend base URL 设置为 `http://localhost:8080/debug` 这样的完整地址；
跨来源时把准确来源传给 `-cors-origins`。

## Docker 健康检查失败

```bash
docker logs weaveflow
docker exec weaveflow wget -q -O - http://127.0.0.1:8080/healthz
```

确认 Web 与 Server 端口不同、`/tmp` 可写且数据卷已挂载。镜像需要预构建的 WebUI，并使用 `/tmp` 生成启动配置。

进一步阅读[运行时模型](/zh/concepts/runtime)、[检查 Run](/zh/guides/observability)、[配置参考](/zh/reference/configuration)
和[可运行示例](/zh/guides/examples)。

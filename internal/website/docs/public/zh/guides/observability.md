# 检查和管理 Run

不要只看返回值，把 Run 当作一条证据时间线。运行时会持久化 Run、每个节点的 Step、有序 Event、Checkpoint 和可选的
Artifact。

## 在 Workbench 中检查

1. 打开 Graph，创建不可变的 Session。
2. 使用尽可能精简的初始 State 启动 Run。
3. 检查状态、Steps、Events、State 和 Checkpoints。
4. 打开 Event 查看关联的节点、任务和数据。
5. 补充输入或解决副作用后，仅从兼容检查点恢复。

## 使用 HTTP 检查

```bash
curl http://127.0.0.1:8080/healthz
curl -H "Authorization: Bearer $WEAVEFLOW_MANAGEMENT_TOKEN" \
  "http://127.0.0.1:8080/graphs/<graph-id>/runs/<run-id>/inspection"
```

Events 和 Artifacts 支持分页读取。管理路由参见 [HTTP API](/zh/reference/http-api)。

## 暂停、恢复和取消

暂停请求会在安全的检查点边界生效。暂停的 Run 会保留最后一个检查点，契约允许时可以带新输入恢复。取消后的 Run 不会
继续执行；请通过最终 Events 区分用户取消和节点失败。副作用结果未知时，先检查 Events 再恢复。

## 数据卫生与指标

不要把 API key、Bearer token 或原始凭据写入 State、Event、Artifact。写入诊断 Artifact 前先脱敏，并为运行数据配置
合理的保留策略。建议关注按 Session 版本统计的耗时、错误分类、暂停/恢复频率、模型延迟、工具失败率和存储体积。

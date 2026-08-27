# 部署调试服务器

开发时可以直接运行 Go 进程；需要稳定、可重复的部署时，可以使用包含 Go 服务、WebUI 和 Nginx 的镜像，配合 Docker Compose 运行。

## 本地进程

```bash
go build ./cmd/server
go run ./cmd/server \
  -addr 127.0.0.1:8080 \
  -data .local/server \
  -graph examples/codespaces_demo/graph.json
```

该命令只启动 API；需要浏览器界面时，请单独运行 [Workbench](/zh/guides/workbench)。服务提供 `GET /healthz` 接口。
反向代理使用路径前缀时加 `-prefix /debug`；监听非回环地址必须设置 `WEAVEFLOW_MANAGEMENT_TOKEN`。

## Docker 镜像

在仓库根目录构建，以便 Dockerfile 访问 Go 包和 WebUI。下面的 `0.1.1` 请替换为实际部署版本：

```bash
docker build --build-arg VERSION=0.1.1 \
  -t weaveflow:0.1.1 -f scripts/Dockerfile .
docker run --detach --init --name weaveflow \
  --restart unless-stopped --read-only \
  --security-opt no-new-privileges:true --cap-drop ALL \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --publish 127.0.0.1:8080:8080 \
  --env-file scripts/.env --volume weaveflow-data:/data \
  --volume "$PWD:/workspace:ro" weaveflow:0.1.1
```

镜像以非 root 用户运行，并将 WebUI 启动配置生成在 `/tmp`，因此可以使用只读根文件系统。只有工作流确实需要写
文件时才将工作区设为可写。

## Compose

```bash
cp scripts/.env.example scripts/.env
docker compose --env-file scripts/.env -f scripts/compose.yaml up -d
docker compose --env-file scripts/.env -f scripts/compose.yaml ps
curl http://127.0.0.1:8080/healthz
```

Compose 使用预构建镜像。复制示例文件后，将 `WEAVEFLOW_IMAGE` 设置为刚构建的 `weaveflow:0.1.1`（或实际 tag），
并替换示例管理令牌。Compose 会提供持久化的 `weaveflow-data` 卷、仅向回环地址发布的端口、健康检查和限制大小的日志。只有在
可信网络边界后才设置 `WEAVEFLOW_PUBLISH_HOST=0.0.0.0`，并始终配置强管理令牌。

## 反向代理与 TLS

在可信代理终止 TLS，并转发 Web 端口。API 挂载到 `/debug` 时设置 `WEAVEFLOW_SERVER_PREFIX=/debug`，并将
Workbench 的 Backend base URL 设置为相同来源和前缀。跨来源部署时，为 `WEAVEFLOW_CORS_ORIGINS` 配置明确白名单。

## 持久化与备份

数据目录保存 Graph、Session、Run、Checkpoint、Event、Artifact、Trigger 和托管密钥。升级前备份数据卷并测试恢复；
需要可回滚时固定镜像 tag，不要使用 `latest`。

## 生产边界

镜像提供进程和文件系统层面的安全默认值，但不提供多租户授权、配额、审计策略或跨 Worker 接管。这些能力，以及
网络策略、密钥轮换、监控和备份策略，应由部署环境补充。

完整的容器变量说明和 Docker CLI 辅助脚本，请参见仓库的 `scripts/README.md`。

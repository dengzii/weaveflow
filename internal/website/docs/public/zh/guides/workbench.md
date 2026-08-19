# Workbench

Workbench 是本地 Graph Definition 编辑器和 Run 调试器。它是位于 `internal/web/` 下的独立 Bun/React 应用，
直接连接调试服务器 API。

## 启动调试服务器

```bash
go run ./cmd/server -data .local/server
```

如需预加载一个图：

```bash
go run ./cmd/server -data .local/server -graph examples/supervisor_mode/graph.json
```

## 启动 WebUI

```bash
cd internal/web
bun install
bun run dev
```

打开命令输出的 URL。应用会跳转到 `/app/graph`，并默认连接 `http://localhost:8080`。当服务器使用其他来源或
路由前缀时，请在 Workbench 设置中修改 Backend base URL。

## 可以检查的内容

- 图节点、边、条件、State Modules 和 State Bindings
- 不可变的 Graph Sessions 和运行时设置
- Run 状态、Events、Checkpoints、Artifacts 和 State
- 等待用户输入或审批的暂停 Run

线上入口为 [playground.weaveflow.space](https://playground.weaveflow.space)。

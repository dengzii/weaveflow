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

打开命令行输出的 URL。应用会跳转到 `/app/graph`，并默认连接 `http://localhost:8080`。当服务器使用其他来源或
路由前缀时，请在 Workbench 设置中修改 Backend base URL。

单来源部署时，直接打开反向代理提供的 WebUI 地址。独立开发服务器则填写包含前缀的完整 API 地址，例如
`http://localhost:8080/debug`。

## 可以检查的内容

- 图节点、边、条件、State Modules 和 State Bindings
- 不可变的 Graph Sessions 和运行时设置
- Run 状态、Events、Checkpoints、Artifacts 和 State
- 等待用户输入或审批的暂停 Run

## 推荐工作流

1. 打开 **Registry**，确认服务器当前的 State Modules、节点、条件、能力和 reducer。
2. 导入或编辑 Graph Definition；在 **State Bindings** 面板设置路径，不要把路径放进节点配置。
3. 创建 Session 前修复 lint 错误；缺少生产者的警告通常需要修改 Trigger 或初始 State。
4. 创建 Session，并记录 Session ID 与定义修订。
5. 使用最小初始 State 运行，再检查 Steps、Events、State、Checkpoints 和 Artifacts。
6. 操作暂停、恢复或取消前，确认当前状态和最后检查点。

JSON 编辑器与结构化检查器共享同一份定义。批量修改后回到检查器，可以及时发现契约诊断。

在线体验地址为 [playground.weaveflow.space](https://playground.weaveflow.space)。

该线上环境适合实验。生产凭据和敏感业务数据应放在你控制的部署环境中。

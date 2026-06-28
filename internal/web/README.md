# WeaveFlow Debug Web

Basic server-facing graph debugging UI.

```bash
bun install
bun run dev
```

Set `WEAVEFLOW_BACKEND` to point the dev proxy at a running `cmd/server` instance.

Common local setup:

```bash
export OPENAI_API_KEY=<your-api-key>
export OPENAI_BASE_URL=<your-base-url>
export OPENAI_MODEL=<your-model>
go run ./cmd/server -addr :8081 -data .local/server
```

```powershell
$env:WEAVEFLOW_BACKEND = "http://127.0.0.1:8081"
$env:DEV_PORT = "3001"
bun run dev
```

Open `/app/graph` on the dev server. The root path redirects to the graph workspace.

The Graph workspace supports local graph drafts in browser `localStorage`, node type palette creation from `/registry`, node/edge editing, upload to `POST /graph`, and run debugging through the server API.

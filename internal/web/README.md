# WeaveFlow Debug Web

Dense server-facing Graph v2 editor and run debugger.

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

The Graph workspace supports local graph drafts in browser `localStorage`, node type palette creation from `/registry`,
node/edge editing, upload to `POST /graph`, and run debugging through the server API.

Graph definitions use version `2.0`. The Graph Inspector selects registered State Modules; Node and edge Condition
Inspectors render a separate State Bindings section from each component's `state_ports`. The path picker filters module
fields, producer outputs, and capability-compatible roots while still allowing a valid custom `shared` or `scopes`
path. Bound capability ports show their final absolute resolved contract. The collapsible Graph JSON editor and the
structured inspectors share one definition source, so edits stay synchronized in both directions. Missing required
bindings, incompatible/reserved paths, unresolved producers, and parallel write conflicts are lint errors that block
save/run.

The Registry dialog exposes State Modules, capabilities, Node State Ports, Condition State Ports, and the generated
Graph JSON Schema. Run Status includes a Run State view grouped by referenced modules, capability roots, `shared`, and
individual `scopes` roots.

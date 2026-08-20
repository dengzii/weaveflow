# WeaveFlow Debug Web

Graph Definition editor and run debugger for the debug server.

```bash
bun install
bun run dev
```

The WebUI and API server run independently. API requests and the runtime event stream connect directly to the configured
backend base URL, which defaults to `http://localhost:8080`.

Common local setup:

```bash
export OPENAI_API_KEY=<your-api-key>
export OPENAI_BASE_URL=<your-base-url>
export OPENAI_MODEL=<your-model>
go run ./cmd/server -addr :8080 -data .local/server
```

```powershell
bun run dev
```

Open `/app/graph` on the dev server. The root path redirects to the graph workspace.

Change the Backend base URL under `/app/settings` when connecting to another API server. The browser override is stored
locally and takes precedence over `config.js`. To set a deployment-wide default, run `bun run build` and edit the
unbundled `dist/config.js`:

```js
window.__WEAVEFLOW_CONFIG__ = {
  backendBaseUrl: "https://api.example.com/debug",
};
```

Serve `dist/` with an SPA fallback to `index.html`. When the WebUI and API use different origins, allow the WebUI origin
on the API server:

```bash
go run ./cmd/server -addr :8080 -prefix /debug -cors-origins https://web.example.com
```

## Workspace

The graph workspace loads graph summaries from the server, fetching full detail only when you select a graph. You can add
nodes from a palette of registered node types, edit node and edge properties, and create immutable Sessions for
execution. The workspace supports:

- Running a Session exactly once and inspecting the results.
- Viewing aggregate run history for a graph.
- Replaying graph-scoped SSE events.

Graph definitions use version `1.0`. The Graph Inspector shows available State Modules; the Node and Condition Inspector
panels render a separate State Bindings section from each component's `state_ports`. The path picker filters module
fields, producer outputs, and capability-compatible roots, and still accepts a valid custom `shared` or `scopes`
path. Bound capability ports display their resolved contract.

The collapsible Graph JSON editor and the structured inspectors share one definition source, so edits stay synchronized
in both directions. Missing required bindings, incompatible or reserved paths, and parallel write conflicts surface as
lint errors. Missing producers appear as warnings. Direct Run input and Trigger contracts are validated separately
before execution or save.

## Registry dialog

The Registry dialog shows State Modules, capabilities, Node State Ports, Condition State Ports, and the generated
Graph JSON Schema.

## Run panel

The bottom Run panel uses resizable columns for the run list, the selected run's events, and the selected event details,
with a default width ratio of `1:1.5:2`.

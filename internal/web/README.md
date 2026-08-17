# WeaveFlow Debug Web

Dense server-facing Graph Definition editor and run debugger.

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

The Graph workspace loads paged summaries from `/graphs`, fetches only the selected Graph detail, and caches hydrated
graphs in memory. It also supports node type palette creation from `/registry`, node/edge editing, immutable Session
creation through `POST /graphs/:graph_id/sessions`, exact-Session asynchronous runs, aggregate Run inspection, and
Graph-scoped SSE replay.

Graph definitions use version `1.0`. The Graph Inspector selects registered State Modules; Node and edge Condition
Inspectors render a separate State Bindings section from each component's `state_ports`. The path picker filters module
fields, producer outputs, and capability-compatible roots while still allowing a valid custom `shared` or `scopes`
path. Bound capability ports show their final absolute resolved contract. The collapsible Graph JSON editor and the
structured inspectors share one definition source, so edits stay synchronized in both directions. Missing required
bindings, incompatible/reserved paths, and parallel write conflicts are lint errors. Missing producers are warnings;
concrete Direct Run input and each Trigger entry contract are validated separately before execution or Trigger save.

The Registry dialog exposes State Modules, capabilities, Node State Ports, Condition State Ports, and the generated
Graph JSON Schema. The bottom Run panel uses resizable columns for the run list, the selected run's events, and the
selected event details, with a default width ratio of `1:1.5:2`.

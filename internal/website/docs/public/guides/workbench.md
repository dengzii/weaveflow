# Workbench

The Workbench is the local Graph Definition editor and Run debugger. It is a separate Bun/React application under
`internal/web/` and connects directly to the debug server API.

## Start the debug server

```bash
go run ./cmd/server -data .local/server
```

To preload a graph:

```bash
go run ./cmd/server -data .local/server -graph examples/supervisor_mode/graph.json
```

## Start the WebUI

```bash
cd internal/web
bun install
bun run dev
```

Open the printed URL. The application redirects to `/app/graph` and connects to `http://localhost:8080` by default.
Change the Backend base URL in Workbench settings when the server uses another origin or route prefix.

## What you can inspect

- Graph nodes, edges, conditions, State Modules, and State Bindings
- Immutable Graph Sessions and runtime settings
- Run status, events, checkpoints, artifacts, and state
- Paused runs that require user input or approval

The hosted entry point is [playground.weaveflow.space](https://playground.weaveflow.space).

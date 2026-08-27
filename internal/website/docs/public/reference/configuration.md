# Configuration Reference

WeaveFlow has three configuration layers: command-line flags, process environment, and Graph/Session settings. Keep
credentials in the environment or managed secret references; Graph Definitions should describe behavior, not secrets.

## Server flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | Listen address. Non-loopback addresses require a management token. |
| `-data` | `.local/wf` | Runtime data directory. |
| `-secret-dir` | empty | Directory for file-backed secret references. |
| `-prefix` | empty | HTTP route prefix such as `/debug`. |
| `-cors-origins` | `*` | Comma-separated browser origins, or `*`. |
| `-graph` | empty | Graph Definition JSON to preload. |
| `-log-level` | `debug` | `debug`, `info`, or `error`. |

The default `-cors-origins=*` accepts requests from any browser origin and is intended for local development. Use an
explicit allowlist for cross-origin production deployments.

Example:

```bash
go run ./cmd/server -addr 127.0.0.1:8080 -data .local/server \
  -secret-dir .local/secrets -prefix /debug -log-level info
```

## Model variables

| Variable | Use |
| --- | --- |
| `OPENAI_API_KEY` | Default OpenAI-compatible credential for model examples. |
| `OPENAI_BASE_URL` | API root, commonly ending in `/v1`. |
| `OPENAI_MODEL` | Default model ID. |
| `WEAVEFLOW_ASSISTANT_API_KEY` | Enables the optional server assistant when paired with its model ID. |
| `WEAVEFLOW_ASSISTANT_MODEL` | Assistant model ID. |
| `WEAVEFLOW_ASSISTANT_BASE_URL` | Assistant provider base URL. |
| `WEAVEFLOW_ASSISTANT_PROVIDER` | Assistant provider profile. |
| `WEAVEFLOW_ASSISTANT_API_FORMAT` | `chat_completions` or `responses`. |

## Security and runtime variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `WEAVEFLOW_MANAGEMENT_TOKEN` | empty | Bearer token protecting management routes. |
| `WEAVEFLOW_TOOL_WORKDIR` | unset | Workspace root for file tools; unset uses the process working directory. |
| `WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK` | `false` | Explicitly bypasses workspace checks; avoid in production. |
| `WEAVEFLOW_BASH_TIMEOUT` | `120000` | Bash tool timeout in milliseconds. |
| `WEAVEFLOW_BASH_ALLOWLIST` | empty | Optional comma-separated command allowlist. |
| `WEAVEFLOW_CODEX_WORKSPACE_ROOTS` | empty | Allowed roots for the Codex runner. |
| `WEAVEFLOW_CLAUDE_WORKSPACE_ROOTS` | empty | Allowed roots for the Claude runner. |

The server settings API can expose environment metadata and secret references without returning secret values. Restrict
management routes, and use a separate secret directory or external secret injection for deployed instances.

## Graph and Session settings

Graph Definitions set topology, State Modules, node/condition configuration, policies, and metadata. Graph Sessions are
immutable execution views that capture the definition revision and runtime settings. Validate a definition against the
current registry before creating a Session.

## Container variables

The packaged image adds `WEAVEFLOW_WEB_PORT`, `WEAVEFLOW_SERVER_PORT`, `WEAVEFLOW_WEB_BACKEND_URL`, `WEAVEFLOW_DATA_DIR`,
`WEAVEFLOW_SECRET_DIR`, `WEAVEFLOW_GRAPH`, `WEAVEFLOW_SERVER_PREFIX`, `WEAVEFLOW_PUBLISH_HOST`, and
`WEAVEFLOW_PUBLISH_PORT`. See [Deploy the Server](/deployment) and `scripts/.env.example` for defaults.

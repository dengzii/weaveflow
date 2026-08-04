# Server API Reference

Use this reference for the current debug server API workflow. Treat `internal/server/routes.go` and the live server as
authoritative if the checkout has changed.

## Base URL And Response Shape

The server normally listens at `http://127.0.0.1:8080`. Append the configured route prefix, if any, exactly once.

All ordinary JSON endpoints return:

```json
{
  "data": {}
}
```

Failures add `error.code` and `error.message`. A failed start or resume may still include a partial run result in`data`;
inspect it before retrying. Exceptions to the JSON envelope are `204 No Content`, Mermaid text, SSE, and raw or
downloaded artifacts.

PowerShell discovery:

```powershell
$weaveflowBaseURL = "http://127.0.0.1:8080"
$registry = Invoke-RestMethod "$weaveflowBaseURL/registry"
$tools = Invoke-RestMethod "$weaveflowBaseURL/runtime/tools"
$settings = Invoke-RestMethod "$weaveflowBaseURL/runtime/settings"
```

Use a task-specific variable such as `$weaveflowBaseURL`; do not repurpose system variables. Do not start the server
just because the probe fails.

## Discovery And Graph Endpoints

| Method | Path                                | Purpose                                                                                      |
|--------|-------------------------------------|----------------------------------------------------------------------------------------------|
| `GET`  | `/registry`                         | Return state modules, capabilities, node types, conditions, graph schema, and chat channels. |
| `GET`  | `/runtime/tools`                    | Return tool IDs and function schemas available to agent nodes.                               |
| `GET`  | `/runtime/settings`                 | Return sanitized process-wide model, environment, and memory settings.                       |
| `PUT`  | `/runtime/settings`                 | Update process-wide runtime settings for future runs.                                        |
| `POST` | `/graph/initial-state-requirements` | Build a candidate and return required initial state without installing it.                   |
| `PUT`  | `/graph`                            | Install a Draft graph session and make it current for `/runs`.                               |
| `POST` | `/graph/publish`                    | Install and mark an Official graph session for triggers.                                     |
| `GET`  | `/graph`                            | Return current graph identity and hashes.                                                    |
| `GET`  | `/graph/definition`                 | Return the normalized current definition.                                                    |
| `GET`  | `/graph/nodes`                      | Return current resolved node specs.                                                          |
| `GET`  | `/graph/initial-state-requirements` | Return requirements for the current graph.                                                   |
| `GET`  | `/graph/mermaid`                    | Return current graph as plain Mermaid text.                                                  |
| `GET`  | `/graphs`                           | Summarize persisted graph sessions by logical graph ID.                                      |

The candidate analysis, draft upload, and publish endpoints all use the same strict envelope, with an 8 MiB limit:

```json
{
  "graph_id": "support-agent",
  "graph_version": "v1",
  "definition": {
    "version": "2.0",
    "state_modules": [],
    "nodes": []
  }
}
```

Unknown fields, extra JSON values, a missing definition, and invalid Graph v2 content are rejected. Build one envelope
and send it first to candidate analysis, then to Draft upload.

```powershell
$definition = Get-Content -Raw ".local/support-agent.json" | ConvertFrom-Json
$envelope = @{
  graph_id = "support-agent"
  graph_version = "v1"
  definition = $definition
} | ConvertTo-Json -Depth 100

Invoke-RestMethod "$weaveflowBaseURL/graph/initial-state-requirements" `
  -Method Post -ContentType "application/json" -Body $envelope

$loaded = Invoke-RestMethod "$weaveflowBaseURL/graph" `
  -Method Put -ContentType "application/json" -Body $envelope
```

Check `data.graph.official`, `graph_hash`, `graph_snapshot_hash`, `graph_session_id`, and `data.warnings`. Draft upload
changes the current runner but does not change the Official graph used by triggers.

## Runtime Settings

Settings are process-wide and affect future runs, not runs already in progress. The request is strict and accepts only
`environment`, `models`, and `memory`.

- Omit a top-level field to preserve it.
- Send `environment: {}` to clear environment values managed by settings.
- Sending `models` replaces the model list. Reconstruct every model that must remain.
- Use model IDs in graph node `config.model_id`. The default ID is `default`.
- Only provider `openai` is supported.
- Enabling a model requires an API key from the request, saved settings, or process environment.
- GET responses never expose API keys; `api_key_configured` only reports presence.
- Do not send response-only fields such as `environment_presets` or `api_key_configured` back to the strict PUT
  endpoint.

Example model update:

```powershell
$modelUpdate = @{
  models = @(
    @{
      id = "default"
      enabled = $true
      provider = "openai"
      model = $env:OPENAI_MODEL
      base_url = $env:OPENAI_BASE_URL
      api_key = $env:OPENAI_API_KEY
    }
  )
} | ConvertTo-Json -Depth 20

Invoke-RestMethod "$weaveflowBaseURL/runtime/settings" `
  -Method Put -ContentType "application/json" -Body $modelUpdate
```

Do not echo `$modelUpdate` when it contains a key.

## Run And Inspection Endpoints

| Method   | Path                                   | Purpose                                                                      |
|----------|----------------------------------------|------------------------------------------------------------------------------|
| `POST`   | `/runs`                                | Synchronously run the current Draft graph.                                   |
| `GET`    | `/runs`                                | List current runs; add `graph_id` for persisted sessions of a logical graph. |
| `GET`    | `/runs/:run_id`                        | Read a run record.                                                           |
| `POST`   | `/runs/:run_id/pause`                  | Request a safe-point pause on the current runner and wait for it.            |
| `POST`   | `/runs/:run_id/resume`                 | Resume the current runner's run from its last checkpoint.                    |
| `POST`   | `/runs/:run_id/cancel`                 | Cancel a run; historical paused runs can use `graph_id`.                     |
| `DELETE` | `/runs/:run_id`                        | Delete a run and associated debug data.                                      |
| `GET`    | `/runs/:run_id/interrupt`              | Return pause, breakpoint, and resume context.                                |
| `GET`    | `/runs/:run_id/steps`                  | Return node attempts and errors.                                             |
| `GET`    | `/runs/:run_id/checkpoints`            | List checkpoint metadata.                                                    |
| `GET`    | `/checkpoints/:checkpoint_id`          | Return checkpoint business and runtime state.                                |
| `POST`   | `/checkpoints/:checkpoint_id/resume`   | Resume the current runner from a selected checkpoint.                        |
| `GET`    | `/runs/:run_id/events`                 | Return persisted non-streaming runtime events.                               |
| `GET`    | `/runs/:run_id/artifacts`              | List artifacts.                                                              |
| `GET`    | `/runs/:run_id/artifacts/:artifact_id` | Read JSON detail; use `format=raw` or `format=download` for bytes.           |

Start and resume bodies are also strict and limited to 8 MiB:

```json
{
  "initial_state": {
    "shared": {
      "request": {
        "input": "Investigate the failure"
      }
    }
  }
}
```

```json
{
  "input": {
    "shared": {
      "request": {
        "approval": "continue"
      }
    }
  }
}
```

`POST /runs` blocks until completed, paused, failed, or canceled. Its successful `data` contains `run`, optional final
`state`, and optional `interrupt`.

For historical reads, append `?graph_id=<run.graph_id>` to the run, step, checkpoint, event, interrupt, and artifact
paths. This prevents a current graph switch from making an existing run appear missing.

## Runtime Event Stream

Subscribe with `GET /runtime/events/stream`. Filter with `run_id`, `node_id`, or repeated/comma-separated `type` values:

```text
/runtime/events/stream?run_id=<id>&type=run.failed,nodes.failed,contract.violation
```

The stream sends an immediate heartbeat and then heartbeats every 15 seconds. It is best-effort, has no replay, and can
drop events for slow consumers. `llm.content_chunk` and `llm.reasoning_chunk` are live-only and are not persisted.
Subscribe before the run when chunks matter, close the stream afterward, and use `/runs/:run_id/events` for durable
history.

## Publication

Publish only after explicit authorization:

```powershell
$published = Invoke-RestMethod "$weaveflowBaseURL/graph/publish" `
  -Method Post -ContentType "application/json" -Body $envelope
```

Verify `data.graph.official` is `true` and record the returned graph session ID. Publication changes which graph future
triggers resolve; it does not execute a run.

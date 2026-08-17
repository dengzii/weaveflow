# Server API Reference

Use this reference for the current Graph-scoped debug server API. Treat `internal/server/routes.go` and the live server
as authoritative if the checkout has changed.

## Contents

- [Base URL And Response Shape](#base-url-and-response-shape)
- [Discovery And Graph Resources](#discovery-and-graph-resources)
- [Session Retention And Trigger Visibility](#session-retention-and-trigger-visibility)
- [Runtime Settings](#runtime-settings)
- [Run And Inspection Resources](#run-and-inspection-resources)
- [Runtime Event Stream](#runtime-event-stream)
- [Trigger Resources](#trigger-resources)
- [Chat Channel Setup](#chat-channel-setup)

## Base URL And Response Shape

The server normally listens at `http://127.0.0.1:8080`. Append the configured route prefix, if any, exactly once.

Ordinary JSON responses use this envelope:

```json
{
  "data": {}
}
```

Failures include `error.code` and `error.message`; a failed resume can also include partial `data`. Exceptions are `204
No Content`, SSE, and raw or downloaded artifacts.

PowerShell discovery:

```powershell
$weaveflowBaseURL = "http://127.0.0.1:8080"
$registry = Invoke-RestMethod "$weaveflowBaseURL/registry"
$tools = Invoke-RestMethod "$weaveflowBaseURL/runtime/tools"
$graphPage = Invoke-RestMethod "$weaveflowBaseURL/graphs?limit=200"
```

Use a task-specific variable such as `$weaveflowBaseURL`; do not repurpose system variables. Do not start the server
just because a probe fails.

## Discovery And Graph Resources

| Method | Path                                                           | Purpose |
|--------|----------------------------------------------------------------|---------|
| `GET`  | `/registry`                                                    | Return state modules, capabilities, node types, conditions, Graph schema, and Chat Channels. |
| `GET`  | `/runtime/tools`                                               | Return tool IDs and function schemas available to agent nodes. |
| `GET`  | `/graphs?limit=&cursor=`                                       | Return lightweight logical Graph summaries as `{items,next_cursor}`. |
| `GET`  | `/graphs/:graph_id`                                            | Return the latest complete Session detail, including definition and sanitized settings. |
| `POST` | `/graphs/:graph_id/analysis/initial-state-requirements`        | Build a candidate and return required initial state without installing it. |
| `POST` | `/graphs/:graph_id/sessions`                                   | Create or reuse one complete immutable execution Session. |

Graph IDs are portable path IDs of at most 200 characters containing only letters, digits, `.`, `_`, or `-`; `.` and
`..` are invalid IDs. The path is the only source of Graph identity; do not repeat `graph_id` in the request body.

Graph lists are deliberately lightweight. Fetch detail lazily for the Graph being inspected or edited. List pages use
an opaque `next_cursor`; continue until it is empty when a complete inventory is required. `limit` defaults to 50 and
cannot exceed 200.

Candidate analysis and Session creation accept the same strict Graph upload envelope, with an 8 MiB limit. `settings`
is required for Session creation but optional during analysis. Analysis decodes the settings shape but does not apply or
validate model providers, credentials, or runtime availability; it builds the Graph and evaluates direct
and optional Trigger initial-state requirements. Do not treat successful analysis as settings validation:

```json
{
  "graph_version": "v1",
  "definition": {
    "version": "1.0",
    "state_modules": [],
    "nodes": []
  },
  "settings": {
    "environment": {},
    "models": []
  }
}
```

Unknown fields, extra JSON values, a missing definition, and invalid Graph Definition content are rejected. Example:

```powershell
$graphID = "support-agent"
$escapedGraphID = [Uri]::EscapeDataString($graphID)
$definition = Get-Content -Raw ".local/support-agent.json" | ConvertFrom-Json
$settings = @{
  environment = @{}
  models = @(
    @{
      id = "default"
      enabled = $true
      provider = "openai"
      api_format = "chat_completions"
      model = $env:OPENAI_MODEL
      base_url = $env:OPENAI_BASE_URL
      extra_body = @{}
      api_key = $env:OPENAI_API_KEY
    }
  )
}
$sessionRequestObject = @{
  graph_version = "v1"
  definition = $definition
  settings = $settings
}
$sessionRequest = $sessionRequestObject | ConvertTo-Json -Depth 100

$requirements = Invoke-RestMethod `
  "$weaveflowBaseURL/graphs/$escapedGraphID/analysis/initial-state-requirements" `
  -Method Post -ContentType "application/json" -Body $sessionRequest

$loaded = Invoke-RestMethod "$weaveflowBaseURL/graphs/$escapedGraphID/sessions" `
  -Method Post -ContentType "application/json" -Body $sessionRequest
```

Read `data.graph.graph_hash`, `graph_snapshot_hash`, `graph_session_id`, `data.settings`, and `data.warnings`. Creating a
Session installs that exact runtime and immediately makes it the latest Session resolved by Triggers for the Graph. An
identical definition, version, and settings can reuse the current Session rather than create a duplicate.

Only candidate analysis consumes an optional top-level `triggers` array to evaluate Trigger-provided initial state.
Session creation does not install that array. Replace the Graph's actual Trigger set through
`PUT /graphs/:graph_id/triggers` after analysis and Session creation.

## Session Retention And Trigger Visibility

The Server retains the latest five complete Sessions for each Graph. Older active Sessions are protected even when they
fall outside that set, then become eligible for pruning after their Runs stop. Pruning removes the Session directory,
including its persisted Runs, events, checkpoints, and artifacts. Avoid repeated exploratory uploads when historical
diagnostics or resume capability must be preserved.

Every successful Session creation is immediately available to Triggers for that Graph ID. There is no separate publish
step. Failed or incomplete Session creation does not replace the latest complete Session.

## Runtime Settings

Settings belong to an immutable Graph Session and affect runs and Triggers created from that Session, not runs already
in progress. There is no global settings resource and no independent settings update endpoint. Read sanitized settings
from `GET /graphs/:graph_id`.

The required `settings` object accepts only `environment` and `models`:

- Omit a field inside `settings` to inherit it from the latest complete Session for the same Graph, or from Server
  defaults when the Graph has no Session.
- Send `environment: {}` to clear non-secret environment values managed by settings. Secret-named values from the
  previous Session are retained, and `OPENAI_MODEL` plus `OPENAI_BASE_URL` are synchronized from the default model.
- Sending `models` replaces the model list. Reconstruct every model that must remain.
- Use model IDs in node `config.model_id`. The default ID is `default`.
- Supported providers are `openai`, `azure`, `deepseek`, `gemini`, `vllm`, `mistral`, `xai`, and `openrouter`.
- Supported `api_format` values are `chat_completions` and `responses`; the default is `chat_completions`.
- Use `extra_body` for provider-specific JSON fields that must accompany model requests.
- A `codex` node is stricter than ordinary model-backed nodes: its selected model must use provider `openai` and
  `api_format: responses`.
- Enabling a model requires an API key from the request, latest same-Graph Session, settings environment, or process
  environment.
- GET and Session responses never expose API keys; `api_key_configured` only reports presence.
- Do not send response-only fields such as `environment_presets` or `api_key_configured`.

Do not echo `$settings`, `$sessionRequestObject`, or `$sessionRequest` when any contains a key. The definition and
settings are persisted before the Session completion manifest, so readers and Triggers ignore incomplete Sessions.
Model API keys and secret-named environment values are stored in plaintext inside the Session's
`runtime-settings.json`; the Server requests file mode `0600` where supported and redacts those values from API
responses. Treat Session creation as credential persistence, not only an HTTP configuration call.

## Run And Inspection Resources

| Method   | Path                                                                  | Purpose |
|----------|-----------------------------------------------------------------------|---------|
| `POST`   | `/graphs/:graph_id/sessions/:session_id/runs`                         | Start that exact Session asynchronously; return `202` with a `RunRecord`. |
| `GET`    | `/graphs/:graph_id/runs?status=&limit=&cursor=`                       | List Graph runs as `{items,next_cursor}`. |
| `GET`    | `/graphs/:graph_id/runs/:run_id/inspection`                           | Return Run, steps, checkpoints, one event page, and optional interrupt together. |
| `POST`   | `/graphs/:graph_id/runs/:run_id/pause`                                | Request a safe-point pause and wait for the paused status. |
| `POST`   | `/graphs/:graph_id/runs/:run_id/resume`                               | Resume from the Run's latest checkpoint. |
| `POST`   | `/graphs/:graph_id/runs/:run_id/cancel`                               | Cancel an active or paused Run. |
| `DELETE` | `/graphs/:graph_id/runs/:run_id`                                      | Delete the Run and its debug data; return `204`. |
| `GET`    | `/graphs/:graph_id/runs/:run_id/events?limit=&cursor=`                | Read persisted events newest first as `{items,next_cursor}`. |
| `GET`    | `/graphs/:graph_id/runs/:run_id/checkpoints/:checkpoint_id`           | Read one checkpoint after verifying it belongs to the Run. |
| `GET`    | `/graphs/:graph_id/runs/:run_id/artifacts`                            | List artifact references. |
| `GET`    | `/graphs/:graph_id/runs/:run_id/artifacts/:artifact_id`               | Read JSON detail; use `format=raw` or `format=download` for bytes. |

Start and resume bodies are strict and limited to 8 MiB:

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

Start returns before execution finishes:

```powershell
$sessionID = $loaded.data.graph.graph_session_id
$runRequest = @{ initial_state = @{ shared = @{ request = @{ input = "Investigate the failure" } } } } |
  ConvertTo-Json -Depth 100
$started = Invoke-RestMethod `
  "$weaveflowBaseURL/graphs/$escapedGraphID/sessions/$sessionID/runs" `
  -Method Post -ContentType "application/json" -Body $runRequest
$runID = $started.data.run_id
$inspection = Invoke-RestMethod `
  "$weaveflowBaseURL/graphs/$escapedGraphID/runs/$runID/inspection?event_limit=500"
```

`RunRecord.graph_id` and `graph_session_id` identify the immutable execution source. Trigger-started runs carry
`origin.type` and `origin.trigger_id`; an absent origin means a direct start. This links Trigger-originated runs without
a second invocation-list request.

Run list pages are newest-first. The default page size is 100 and the maximum is 500. `status` accepts repeated or
comma-separated values. Inspection uses `event_limit` (default 500, maximum 2000) and `event_cursor`. Persisted event
pages are newest-first and use their own opaque cursor; they are independent of SSE event-ID cursors.

Pause and cancel wait for a safe-point transition. Resume returns a Run result with optional final state and interrupt,
and can block until the resumed execution completes, pauses, fails, or is canceled.

## Runtime Event Stream

Subscribe to the Graph-scoped stream:

```text
GET /graphs/:graph_id/events/stream
```

Optional filters are `session_id`, `run_id`, `node_id`, and repeated or comma-separated `type`. Reconnect by supplying
the last SSE event ID either as `Last-Event-ID` or `cursor`; when both are present they must match.

```text
/graphs/support-agent/events/stream?session_id=<id>&run_id=<id>&type=run.failed,nodes.failed
```

The stream sends an immediate heartbeat and then heartbeats every 15 seconds. Its replay history is bounded and held
only in the current process. A slow subscriber whose channel overflows is disconnected instead of silently dropping a
subset of events. `llm.content_chunk` and `llm.reasoning_chunk` remain live-only and are not persisted. Reconcile live
observation with Run inspection and persisted event pages.

## Trigger Resources

| Method | Path                                                          | Purpose |
|--------|---------------------------------------------------------------|---------|
| `GET`  | `/graphs/:graph_id/triggers`                                  | List sanitized Trigger definitions for one Graph. |
| `PUT`  | `/graphs/:graph_id/triggers`                                  | Atomically replace the complete Trigger set for one Graph. |
| `POST` | `/graphs/:graph_id/triggers/:trigger_id/invocations`          | Invoke Webhook or Schedule semantics; return `202` with `data.run`. |
| `POST` | `/graphs/:graph_id/triggers/:trigger_id/webhook`              | Invoke a Webhook Trigger; return `202` with `data.run`. |
| `POST` | `/graphs/:graph_id/triggers/:trigger_id/chat`                 | Invoke a Chat Trigger. |

`PUT` accepts `{"triggers":[...]}` and is all-or-nothing. Send the complete intended Graph set, including unchanged
Triggers. An empty array removes all Triggers for that Graph. Responses redact Webhook keys and Chat credentials.

Webhook authentication uses `Authorization: Bearer <secret>`. Do not put secrets in query strings. Trigger execution
resolves the latest complete Session for the path Graph ID; failed or incomplete Session creation never replaces it.

## Chat Channel Setup

| Method   | Path                                                                         | Purpose |
|----------|------------------------------------------------------------------------------|---------|
| `POST`   | `/chat-channels/:channel_id/setup-sessions`                                  | Start setup, optionally with `{"trigger_id":"..."}`. |
| `GET`    | `/chat-channels/:channel_id/setup-sessions/:session_id`                      | Read setup status without submitting input. |
| `POST`   | `/chat-channels/:channel_id/setup-sessions/:session_id/verification`         | Submit `{"verification_code":"..."}`. |
| `DELETE` | `/chat-channels/:channel_id/setup-sessions/:session_id`                      | Cancel setup; return `204`. |

When atomically replacing Triggers, pass a ready setup result as `chat_setup_session_id` on its Chat Trigger. The Server
claims the credentials and commits them only if the complete Trigger replacement succeeds.

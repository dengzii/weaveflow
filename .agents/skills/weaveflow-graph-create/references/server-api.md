# Creation API

Use Graph-scoped routes and the management Bearer token. Successful responses wrap data in `data`; failures include `error.code` and `error.message`. Never log Authorization headers or credential values.

## Discovery And Analysis

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/registry` | Read graph schema, node types, conditions, state modules, capabilities, and channels. |
| `GET` | `/runtime/tools` | Read currently registered tools. |
| `GET` | `/graphs` | List Graphs and Session summaries. |
| `GET` | `/graphs/:graph_id` | Read the target Graph's latest complete Session detail. |
| `GET` | `/graphs/:graph_id/sessions/:session_id` | Read one exact immutable Session's definition, settings, required state, identity, latest-Session marker, and registry-drift warnings. |
| `POST` | `/graphs/:graph_id/analysis/initial-state-requirements` | Build a candidate definition and calculate direct or Trigger initial-state requirements without installing it. |

Analysis accepts the strict Graph upload envelope. It may include `definition`, `graph_version`, and Trigger candidates. It does not apply model settings or install a Session.

## Session Installation

| Method | Route | Purpose |
| --- | --- | --- |
| `POST` | `/graphs/:graph_id/sessions` | Validate settings, install, and persist one complete immutable Session. |

The request includes `definition`, `graph_version`, `settings`, and a required `request_id`; commit mode and `expected_graph_session_id` are used for create/overwrite concurrency when required by the API. Settings include models, environment, environment secrets, tool permissions, and tool approvals. Omitted settings may inherit from the latest complete Session or server defaults according to the API contract.

Session creation may persist model API keys and secret-named environment values in the Session directory. Responses redact them and expose only presence indicators. Treat creation as credential persistence, not a harmless configuration preview.

An exact historical Session response may contain `context_warnings` when its persisted definition no longer builds identically against the server's current registry. Preserve the persisted definition and manifest hashes as historical truth; treat currently derived requirements as unavailable when the response cannot safely recompute them.

The server retains the latest five complete Sessions per Graph. Older active Sessions are temporarily protected; inactive older Sessions can later be pruned with their Run diagnostics and resume source.

## Chat Channel Setup

| Method | Route | Purpose |
| --- | --- | --- |
| `POST` | `/chat-channels/:channel_id/setup-sessions` | Start an external Chat Channel setup session. |
| `GET` | `/chat-channels/:channel_id/setup-sessions/:session_id` | Poll sanitized setup status. |
| `POST` | `/chat-channels/:channel_id/setup-sessions/:session_id/verification` | Submit a required verification code. |
| `DELETE` | `/chat-channels/:channel_id/setup-sessions/:session_id` | Cancel an unused setup session. |

Pass only a confirmed `chat_setup_session_id` into the matching Chat Trigger payload. The server claims setup credentials only if complete Trigger replacement succeeds.

## Trigger Management And Invocation

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/graphs/:graph_id/triggers` | Read sanitized Trigger definitions. |
| `PUT` | `/graphs/:graph_id/triggers` | Atomically replace the complete Trigger set. |
| `POST` | `/graphs/:graph_id/triggers/:trigger_id/invocations` | Invoke a Webhook or Schedule Trigger through the unified route. |
| `POST` | `/graphs/:graph_id/triggers/:trigger_id/webhook` | Invoke a Webhook Trigger with request body and headers. |
| `POST` | `/graphs/:graph_id/triggers/:trigger_id/chat` | Invoke a Chat Trigger with its message payload. |

Management routes use management authentication. Public Trigger routes use the Trigger's configured credential and `Authorization: Bearer <secret>` when required. Never put a Trigger secret in a query string. An empty replacement array removes all Triggers, so read and preserve unrelated entries first.

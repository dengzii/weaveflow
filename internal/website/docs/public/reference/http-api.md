# HTTP API Reference

The Debug Server provides a JSON API for Graph Definitions, immutable Sessions, Runs, runtime inspection, registry
discovery, memory, chat channels, and Triggers. All paths below are relative to the optional `-prefix`.

## Authentication

`GET /healthz` and public Trigger invocation routes do not use management authentication. Graph, Run, registry, memory,
assistant, and Trigger management routes require:

```http
Authorization: Bearer <WEAVEFLOW_MANAGEMENT_TOKEN>
```

Use HTTPS or a private network when sending the token. A non-loopback server refuses to start without one.

## Graph lifecycle

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/graphs` | List graph summaries. |
| `GET` | `/graphs/:graph_id` | Read graph detail and registry-resolved metadata. |
| `DELETE` | `/graphs/:graph_id` | Delete a Graph and its retained runtime data. |
| `GET` | `/graphs/:graph_id/retention-audit` | Review retained data before deletion or cleanup. |
| `POST` | `/graphs/:graph_id/sessions` | Create an immutable Graph Session. |
| `GET` | `/graphs/:graph_id/sessions/:session_id` | Read Session detail. |
| `POST` | `/graphs/:graph_id/analysis/initial-state-requirements` | Analyze required initial State. |

Definitions are validated before a Session is created. Treat the returned Session ID as the execution identity.

## Runtime and registry

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/registry` | Discover node, condition, module, capability, and reducer contracts. |
| `GET` | `/runtime/tools` | List tools installed in the runtime context. |

## Runs and evidence

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/graphs/:graph_id/sessions/:session_id/runs` | Start a Run. |
| `GET` | `/graphs/:graph_id/runs` | List Runs for a graph. |
| `GET` | `/graphs/:graph_id/runs/:run_id/inspection` | Read summary, Steps, State, and diagnostics. |
| `GET` | `/graphs/:graph_id/runs/:run_id/events` | Read paged Events. |
| `GET` | `/graphs/:graph_id/events/stream` | Stream graph-scoped runtime Events with SSE. |
| `GET` | `/graphs/:graph_id/runs/:run_id/checkpoints/:checkpoint_id` | Read a checkpoint. |
| `GET` | `/graphs/:graph_id/runs/:run_id/artifacts` | List Run Artifacts. |
| `GET` | `/graphs/:graph_id/runs/:run_id/artifacts/:artifact_id` | Read one Artifact. |
| `POST` | `/graphs/:graph_id/runs/:run_id/pause` | Request a safe pause. |
| `POST` | `/graphs/:graph_id/runs/:run_id/resume` | Resume from a compatible checkpoint. |
| `POST` | `/graphs/:graph_id/runs/:run_id/steps/:step_id/effect-resolution` | Record the outcome of an unresolved side effect. |
| `POST` | `/graphs/:graph_id/runs/:run_id/cancel` | Cancel a Run. |
| `DELETE` | `/graphs/:graph_id/runs/:run_id` | Delete a Run and its retained evidence. |
| `POST` | `/graphs/:graph_id/runs/:run_id/forks` | Fork a Run from retained evidence. |
| `GET` | `/graphs/:graph_id/runs/:run_id/compare/:other_run_id` | Compare two Runs. |

Request and response bodies are versioned with the server. Use the registry and a created Session to discover the exact
State and settings accepted by a graph instead of assuming every node has the same input shape.

## Memory, Assistant, and chat

Memory management uses `/memory/:namespace` for list, search, read, write, and delete operations. The optional Assistant
service uses `/assistant/status`, `/assistant/sessions/:session_id`, `/assistant/sessions/:session_id/messages`,
`/assistant/jobs/:job_id`, and `/assistant/jobs/:job_id/stream`. Chat channel setup uses:

```text
/chat-channels/:channel_id/setup-sessions
/chat-channels/:channel_id/setup-sessions/:session_id/verification
```

## Triggers

Trigger management lives at `/graphs/:graph_id/triggers` (`GET` to list and `PUT` to replace). Public invocation endpoints are:

- `POST /graphs/:graph_id/triggers/:trigger_id/invocations`
- `POST /graphs/:graph_id/triggers/:trigger_id/webhook`
- `POST /graphs/:graph_id/triggers/:trigger_id/chat`

Validate channel credentials through the setup workflow before replacing a graph's Trigger configuration.

## Health and errors

```bash
curl -i http://127.0.0.1:8080/healthz
```

The health response includes server status, version, and UTC build time. Errors are JSON responses with a status-specific
message; preserve the response body and related Event IDs when opening an incident.

# Inspect and Operate Runs

Do not treat a Run as just a return value. The runtime records a durable Run record, per-node Steps, ordered Events,
Checkpoints, and optional Artifacts as an evidence timeline.

## Inspect in the Workbench

1. Open a Graph and create an immutable Session.
2. Start a Run with the smallest useful initial State.
3. Select the Run to inspect status, Steps, Events, State, and Checkpoints.
4. Open an Event to see its payload and related node or task.
5. Resume only from a compatible checkpoint after supplying required input or resolving an effect.

The event stream is graph-scoped, so the Workbench can follow new Runs without losing historical records.

## Inspect with HTTP

The Debug Server exposes the same records over HTTP. A typical read-only investigation is:

```bash
curl http://127.0.0.1:8080/healthz
curl -H "Authorization: Bearer $WEAVEFLOW_MANAGEMENT_TOKEN" \
  http://127.0.0.1:8080/graphs
curl -H "Authorization: Bearer $WEAVEFLOW_MANAGEMENT_TOKEN" \
  "http://127.0.0.1:8080/graphs/<graph-id>/runs/<run-id>/inspection"
```

Use pagination for Events and Artifacts. The [HTTP API reference](/reference/http-api) lists management and public
Trigger routes.

## Pause, resume, and cancel

Pause requests take effect at a safe checkpoint boundary. A paused Run retains its last checkpoint and can be resumed with
new input when the node contract allows it. Cancel is terminal; inspect the final Events to distinguish a requested cancel
from a node failure.

## Event and artifact hygiene

Do not put API keys, bearer tokens, or raw provider credentials in state, Events, or Artifacts. Redact sensitive payloads
before writing diagnostic artifacts. Keep retention policies appropriate for the data stored in `.local/` or the server
data volume.

## Useful operational signals

- Run status and duration by Graph Session revision.
- Failed node, condition, and error class counts.
- Pause/resume frequency and checkpoint age.
- Model latency, token usage, and tool failure rates from Event payloads.
- Artifact and runtime-store size, plus retention cleanup results.

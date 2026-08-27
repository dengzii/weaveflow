#!/usr/bin/env python3
"""Build a sanitized, read-only quality report for one Graph Run."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from typing import Any


class ApiError(RuntimeError):
    """Raised when a public API request cannot produce JSON data."""


def unwrap_data(payload: Any) -> Any:
    if isinstance(payload, dict) and "data" in payload:
        return payload["data"]
    return payload


def sanitize_message(value: Any) -> str:
    message = str(value or "")
    message = re.sub(r"(?i)(bearer\s+)[^\s]+", r"\1<redacted>", message)
    message = re.sub(
        r"(?i)((?:api[_-]?key|token|secret|password)\s*[:=]\s*)[^\s,;]+",
        r"\1<redacted>",
        message,
    )
    if len(message) > 240:
        return message[:240] + "..."
    return message


def request_json(base_url: str, path: str, token: str) -> Any:
    request = urllib.request.Request(base_url.rstrip("/") + path, method="GET")
    request.add_header("Accept", "application/json")
    if token:
        request.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            body = response.read()
    except urllib.error.HTTPError as error:
        body = error.read()
        try:
            details = json.loads(body.decode("utf-8", errors="replace"))
            details = unwrap_data(details)
            if isinstance(details, dict):
                message = details.get("message") or details.get("error")
            else:
                message = details
        except (UnicodeDecodeError, json.JSONDecodeError):
            message = "HTTP request failed"
        raise ApiError(f"HTTP {error.code}: {sanitize_message(message)}") from error
    except urllib.error.URLError as error:
        raise ApiError(f"network error: {sanitize_message(error.reason)}") from error
    try:
        return unwrap_data(json.loads(body.decode("utf-8")))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ApiError("API returned invalid JSON") from error


def path_value(value: Any, path: tuple[str, ...]) -> Any:
    current = value
    for key in path:
        if not isinstance(current, dict):
            return None
        current = current.get(key)
    return current


def as_list(value: Any) -> list[Any]:
    if isinstance(value, list):
        return value
    if isinstance(value, dict) and isinstance(value.get("items"), list):
        return value["items"]
    return []


def fetch_events(
    base_url: str, graph_id: str, run_id: str, token: str, limit: int
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    events: list[dict[str, Any]] = []
    seen_ids: set[str] = set()
    seen_cursors: set[str] = set()
    cursor: str | None = None
    page_counts: list[int] = []
    cursor_loop = False
    while True:
        query = {"limit": str(limit)}
        if cursor is not None:
            query["cursor"] = cursor
        path = (
            f"/graphs/{urllib.parse.quote(graph_id, safe='')}/runs/"
            f"{urllib.parse.quote(run_id, safe='')}/events?"
            f"{urllib.parse.urlencode(query)}"
        )
        page = request_json(base_url, path, token)
        page_items = [item for item in as_list(page) if isinstance(item, dict)]
        page_counts.append(len(page_items))
        for event in page_items:
            event_id = str(event.get("id") or "")
            if event_id and event_id in seen_ids:
                continue
            if event_id:
                seen_ids.add(event_id)
            events.append(event)
        next_cursor = page.get("next_cursor") if isinstance(page, dict) else None
        if next_cursor in (None, "", cursor):
            break
        next_cursor_text = str(next_cursor)
        if next_cursor_text in seen_cursors:
            cursor_loop = True
            break
        seen_cursors.add(next_cursor_text)
        cursor = next_cursor_text
    return events, {
        "pages": len(page_counts),
        "page_item_counts": page_counts,
        "cursor_loop": cursor_loop,
        "deduplicated_events": len(events),
    }


def event_type_counts(events: list[dict[str, Any]]) -> dict[str, int]:
    return dict(sorted(Counter(str(event.get("type") or "unknown") for event in events).items()))


def event_failures(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    failures: list[dict[str, Any]] = []
    for event in sorted(events, key=lambda item: str(item.get("timestamp") or "")):
        payload = event.get("payload")
        if not isinstance(payload, dict):
            payload = {}
        event_type = str(event.get("type") or "")
        status = str(payload.get("effect_status") or payload.get("status") or "")
        if event_type in {"tool.denied", "tool.failed", "run.failed"} or status == "unknown":
            failures.append(
                {
                    "timestamp": event.get("timestamp"),
                    "type": event_type,
                    "node_id": event.get("node_id"),
                    "step_id": event.get("step_id"),
                    "name": payload.get("name"),
                    "status": status,
                    "message": sanitize_message(
                        payload.get("error") or payload.get("message") or payload.get("reason")
                    ),
                }
            )
    return failures[:20]


def lifecycle(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    result: list[dict[str, Any]] = []
    for event in sorted(events, key=lambda item: str(item.get("timestamp") or "")):
        event_type = str(event.get("type") or "")
        if event_type.startswith("run."):
            result.append({"timestamp": event.get("timestamp"), "type": event_type})
    return result


def effect_summary(events: list[dict[str, Any]]) -> dict[str, Any]:
    intents: set[str] = set()
    outcomes: set[str] = set()
    status_counts: Counter[str] = Counter()
    denied_or_failed = 0
    unknown = 0
    for event in events:
        payload = event.get("payload")
        if not isinstance(payload, dict):
            continue
        status = str(payload.get("effect_status") or payload.get("status") or "")
        if status:
            status_counts[status] += 1
        if status == "unknown":
            unknown += 1
        if event.get("type") == "effect.intent":
            key = payload.get("key") or payload.get("idempotency_key") or payload.get("operation_key")
            if key:
                intents.add(str(key))
        if event.get("type") == "effect.outcome":
            key = payload.get("key") or payload.get("idempotency_key") or payload.get("operation_key")
            if key:
                outcomes.add(str(key))
        if event.get("type") in {"tool.denied", "tool.failed"}:
            denied_or_failed += 1
    missing_outcomes = len(intents - outcomes)
    if unknown:
        status = "fail"
    elif denied_or_failed or missing_outcomes:
        status = "warn"
    else:
        status = "pass"
    return {
        "status": status,
        "intent_count": len(intents),
        "outcome_count": len(outcomes),
        "missing_outcome_count": missing_outcomes,
        "unknown_count": unknown,
        "denied_or_failed_count": denied_or_failed,
        "status_counts": dict(sorted(status_counts.items())),
    }


def truthy(value: Any) -> bool:
    return value is True or str(value).lower() in {"1", "true", "yes"}


def policy_summary(
    session: dict[str, Any], registry_tools: list[dict[str, Any]], events: list[dict[str, Any]]
) -> dict[str, Any]:
    definition = session.get("definition")
    nodes = as_list(definition.get("nodes") if isinstance(definition, dict) else None)
    declared: set[str] = set()
    for node in nodes:
        config = node.get("config") if isinstance(node, dict) else None
        if isinstance(config, dict):
            declared.update(str(tool_id) for tool_id in config.get("tool_ids") or [])
    observed = set()
    for event in events:
        payload = event.get("payload")
        if event.get("type", "").startswith("tool.") and isinstance(payload, dict) and payload.get("name"):
            observed.add(str(payload["name"]))
    tool_map = {str(tool.get("id")): tool for tool in registry_tools if isinstance(tool, dict)}
    settings = session.get("settings") if isinstance(session.get("settings"), dict) else {}
    permissions = {str(permission) for permission in settings.get("tool_permissions") or []}
    approvals = settings.get("tool_approvals") if isinstance(settings.get("tool_approvals"), dict) else {}
    mismatches: list[dict[str, Any]] = []
    for tool_id in sorted(declared | observed):
        tool = tool_map.get(tool_id)
        if tool is None:
            mismatches.append({"tool_id": tool_id, "kind": "unregistered_tool"})
            continue
        required_permissions = {str(permission) for permission in tool.get("permissions") or []}
        missing_permissions = sorted(required_permissions - permissions)
        if missing_permissions:
            mismatches.append(
                {
                    "tool_id": tool_id,
                    "kind": "missing_permission",
                    "permissions": missing_permissions,
                }
            )
        if tool.get("approval") == "required" and not truthy(approvals.get(tool_id)):
            mismatches.append({"tool_id": tool_id, "kind": "missing_required_approval"})
    status = "fail" if mismatches else "pass"
    return {
        "status": status,
        "declared_tools": sorted(declared),
        "observed_tools": sorted(observed),
        "mismatches": mismatches,
    }


def business_summary(checkpoint: dict[str, Any] | None) -> dict[str, Any]:
    snapshot = checkpoint.get("snapshot") if isinstance(checkpoint, dict) else None
    plan = path_value(snapshot, ("shared", "plan"))
    if not isinstance(plan, dict):
        return {"status": "unknown", "reason": "no observable business contract"}
    steps = [step for step in as_list(plan.get("steps")) if isinstance(step, dict)]
    step_statuses = Counter(str(step.get("status") or "unknown") for step in steps)
    plan_status = str(plan.get("status") or "unknown")
    completed_statuses = {"succeeded", "completed", "done"}
    all_steps_completed = bool(steps) and all(
        str(step.get("status") or "unknown") in completed_statuses for step in steps
    )
    if plan_status == "done" and all_steps_completed:
        status = "pass"
    elif plan_status in {"failed", "pending", "planning", "executing", "replan", "finalizing"}:
        status = "fail"
    else:
        status = "unknown"
    final_answer = path_value(snapshot, ("shared", "final", "answer"))
    if not final_answer:
        final_answer = plan.get("final_answer")
    return {
        "status": status,
        "plan_status": plan_status,
        "step_status_counts": dict(sorted(step_statuses.items())),
        "step_count": len(steps),
        "final_answer_present": bool(final_answer),
        "final_answer_length": len(str(final_answer or "")),
    }


def runtime_summary(run: dict[str, Any], steps: list[dict[str, Any]]) -> dict[str, Any]:
    status = str(run.get("status") or "unknown")
    step_statuses = Counter(str(step.get("status") or "unknown") for step in steps)
    lease = run.get("execution_lease") if isinstance(run.get("execution_lease"), dict) else {}
    if status == "completed" and not step_statuses.get("failed") and lease.get("status") == "released":
        quality_status = "pass"
    elif status in {"failed", "canceled", "cancelled", "error"} or step_statuses.get("failed"):
        quality_status = "fail"
    else:
        quality_status = "unknown"
    return {
        "status": quality_status,
        "run_status": status,
        "step_status_counts": dict(sorted(step_statuses.items())),
        "lease_status": lease.get("status"),
        "started_at": run.get("started_at"),
        "finished_at": run.get("finished_at"),
    }


def evidence_summary(
    final_checkpoint: dict[str, Any] | None,
    artifacts: list[dict[str, Any]],
    events: list[dict[str, Any]],
) -> dict[str, Any]:
    snapshot = final_checkpoint.get("snapshot") if isinstance(final_checkpoint, dict) else None
    answer = path_value(snapshot, ("shared", "final", "answer"))
    if not answer:
        answer = path_value(snapshot, ("shared", "plan", "final_answer"))
    references_present = bool(re.search(r"\[[^\]]+:[^\]]+\]", str(answer or "")))
    artifact_created = sum(1 for event in events if event.get("type") == "artifact.created")
    if not answer:
        status = "unknown"
    elif not artifacts or not artifact_created:
        status = "warn"
    elif references_present:
        status = "pass"
    else:
        status = "warn"
    type_counts = Counter(str(artifact.get("type") or "unknown") for artifact in artifacts)
    return {
        "status": status,
        "final_answer_present": bool(answer),
        "final_answer_evidence_refs": references_present,
        "artifact_count": len(artifacts),
        "artifact_created_event_count": artifact_created,
        "artifact_type_counts": dict(sorted(type_counts.items())),
    }


def overall_status(quality: dict[str, dict[str, Any]]) -> str:
    statuses = [str(item.get("status")) for item in quality.values()]
    if "fail" in statuses:
        return "fail"
    if any(status in {"warn", "unknown"} for status in statuses):
        return "warn"
    return "pass"


def build_report(args: argparse.Namespace) -> dict[str, Any]:
    token = os.environ.get(args.token_env, "") if args.token_env else ""
    graph_id = args.graph_id
    run_id = args.run_id
    encoded_graph = urllib.parse.quote(graph_id, safe="")
    encoded_run = urllib.parse.quote(run_id, safe="")
    inspection = request_json(
        args.base_url,
        f"/graphs/{encoded_graph}/runs/{encoded_run}/inspection",
        token,
    )
    run = inspection.get("run") if isinstance(inspection, dict) else {}
    if not isinstance(run, dict):
        run = {}
    steps = [step for step in as_list(inspection.get("steps")) if isinstance(step, dict)]
    session_id = str(run.get("graph_session_id") or "")
    session: dict[str, Any] = {}
    errors: list[str] = []
    if session_id:
        try:
            session = request_json(
                args.base_url,
                f"/graphs/{encoded_graph}/sessions/{urllib.parse.quote(session_id, safe='')}",
                token,
            )
        except ApiError as error:
            errors.append(f"session: {error}")
    try:
        latest_graph = request_json(args.base_url, f"/graphs/{encoded_graph}", token)
    except ApiError as error:
        latest_graph = {}
        errors.append(f"latest_graph: {error}")
    try:
        registry = request_json(args.base_url, "/registry", token)
    except ApiError as error:
        registry = {}
        errors.append(f"registry: {error}")
    try:
        runtime_tools = request_json(args.base_url, "/runtime/tools", token)
    except ApiError as error:
        runtime_tools = {}
        errors.append(f"runtime_tools: {error}")
    events, pagination = fetch_events(args.base_url, graph_id, run_id, token, args.limit)
    artifacts_payload = request_json(
        args.base_url,
        f"/graphs/{encoded_graph}/runs/{encoded_run}/artifacts",
        token,
    )
    artifacts = [item for item in as_list(artifacts_payload) if isinstance(item, dict)]
    final_checkpoint: dict[str, Any] | None = None
    checkpoint_id = str(run.get("last_checkpoint_id") or "")
    if checkpoint_id:
        try:
            final_checkpoint = request_json(
                args.base_url,
                f"/graphs/{encoded_graph}/runs/{encoded_run}/checkpoints/"
                f"{urllib.parse.quote(checkpoint_id, safe='')}",
                token,
            )
        except ApiError as error:
            errors.append(f"checkpoint: {error}")
    session_graph = session.get("graph") if isinstance(session.get("graph"), dict) else {}
    latest_graph_record = latest_graph.get("graph") if isinstance(latest_graph, dict) else None
    latest_session_id = (
        str(latest_graph_record.get("graph_session_id") or "")
        if isinstance(latest_graph_record, dict)
        else ""
    )
    identity = {
        "graph_id": graph_id,
        "run_id": run_id,
        "graph_session_id": session_id,
        "graph_version": run.get("graph_version"),
        "graph_hash": run.get("graph_hash"),
        "graph_snapshot_hash": run.get("graph_snapshot_hash"),
        "latest_graph_session_id": latest_session_id,
        "session_graph_hash": session_graph.get("graph_hash"),
        "session_snapshot_hash": session_graph.get("graph_snapshot_hash"),
        "last_checkpoint_id": checkpoint_id,
    }
    registry_tools = as_list(runtime_tools.get("tools") if isinstance(runtime_tools, dict) else None)
    quality: dict[str, dict[str, Any]] = {
        "runtime": runtime_summary(run, steps),
        "business": business_summary(final_checkpoint),
        "evidence": evidence_summary(final_checkpoint, artifacts, events),
        "side_effects": effect_summary(events),
    }
    policy = policy_summary(session, registry_tools, events)
    quality["policy"] = policy
    quality["overall"] = {"status": overall_status(quality)}
    return {
        "identity": identity,
        "quality": quality,
        "pagination": pagination,
        "events": {
            "count": len(events),
            "type_counts": event_type_counts(events),
            "lifecycle": lifecycle(events),
            "failures_or_denials": event_failures(events),
        },
        "business": quality["business"],
        "policy": policy,
        "artifacts": quality["evidence"],
        "errors": errors,
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--graph-id", required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument(
        "--token-env",
        default="WEAVEFLOW_MANAGEMENT_TOKEN",
        help="Environment variable containing the management token; empty means unauthenticated.",
    )
    parser.add_argument("--limit", type=int, default=500)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.limit < 1 or args.limit > 500:
        print("--limit must be between 1 and 500", file=sys.stderr)
        return 2
    try:
        report = build_report(args)
    except ApiError as error:
        print(sanitize_message(error), file=sys.stderr)
        return 1
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

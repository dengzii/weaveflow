import { useState } from "react";
import type { ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { cn, formatDateTimeMs, stringifyJSON } from "../../lib/utils";
import type { RuntimeEvent } from "../../types";
import { JSONTree, parseJSONTreeValue } from "./JSONTree";
import { eventTone } from "./runStatusModel";
import { StatusText } from "./shared";

export function RunEventDetail({ event }: { event: RuntimeEvent }) {
  const payload = payloadRecord(event.payload);
  const details = eventPayloadDetails(event, payload);
  const hasPayload = payload
    ? Object.keys(payload).length > 0
    : event.payload !== undefined && event.payload !== null;

  return (
    <div className="grid gap-2 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <StatusText tone={eventTone(event.type)}>{event.type}</StatusText>
        <span className="font-mono text-muted-foreground">{event.node_id || event.run_id}</span>
        <span className="ml-auto tabular-nums text-muted-foreground" title={event.timestamp}>
          {formatDateTimeMs(event.timestamp)}
        </span>
      </div>
      <DetailRow label="Event" value={event.id} />
      {event.graph_session_id ? <DetailRow label="Graph session" value={event.graph_session_id} /> : null}
      <DetailRow label="Run" value={event.run_id} />
      {event.parent_run_id ? <DetailRow label="Parent run" value={event.parent_run_id} /> : null}
      {event.step_id ? <DetailRow label="Step" value={event.step_id} /> : null}
      {event.task_id ? <DetailRow label="Task" value={event.task_id} /> : null}
      {event.node_id ? <DetailRow label="Node" value={event.node_id} /> : null}
      {event.namespace ? <DetailRow label="Namespace" value={event.namespace} /> : null}
      {details.fields.length > 0 ? (
        <DetailSection title="Details">
          <PayloadFields fields={details.fields} />
        </DetailSection>
      ) : null}
      {details.sections}
      {details.additionalFields.length > 0 ? (
        <DetailSection title="Additional details">
          <PayloadFields fields={details.additionalFields} />
        </DetailSection>
      ) : null}
      {hasPayload ? <RawPayloadSection key={event.id} payload={event.payload} /> : null}
    </div>
  );
}

function DetailSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <div className="mb-1 text-xs font-semibold text-muted-foreground">{title}</div>
      <div className="grid gap-1">{children}</div>
    </div>
  );
}

function RawPayloadSection({ payload }: { payload: unknown }) {
  const [expanded, setExpanded] = useState(false);

  return (
    <div>
      <button
        type="button"
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
        className="mb-1 flex w-full items-center gap-2 text-left text-xs font-semibold text-muted-foreground hover:text-foreground"
      >
        <span>Raw payload</span>
        <ChevronDown className={cn("h-3.5 w-3.5 transition-transform", expanded && "rotate-180")} />
      </button>
      {expanded ? (
        <pre className="max-h-72 overflow-auto rounded-md border border-border bg-background p-2 text-[11px]">
          {stringifyJSON(payload)}
        </pre>
      ) : null}
    </div>
  );
}

function DetailRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className="truncate font-mono">{value}</span>
    </div>
  );
}

interface PayloadField {
  label: string;
  value: unknown;
  multiline?: boolean;
}

interface EventPayloadDetails {
  fields: PayloadField[];
  sections: ReactNode;
  additionalFields: PayloadField[];
}

function PayloadFields({ fields }: { fields: PayloadField[] }) {
  return (
    <div className="grid gap-1">
      {fields.map((field) => {
        const treeValue = parseJSONTreeValue(field.value);
        const multiline = field.multiline || isMultilinePayloadValue(field.value);
        return (
          <div
            key={field.label}
            className={cn(
              "grid gap-1 rounded border border-border bg-muted/30 px-2 py-1",
              treeValue || multiline ? "" : "grid-cols-[120px_minmax(0,1fr)] items-center"
            )}
          >
            <span className="text-muted-foreground">{field.label}</span>
            {treeValue ? (
              <JSONTree value={treeValue} label={`${field.label} JSON tree`} scrollable={false} />
            ) : (
              <span className={cn("min-w-0 font-mono", multiline ? "whitespace-pre-wrap break-words" : "truncate")}>
                {formatPayloadValue(field.value)}
              </span>
            )}
          </div>
        );
      })}
    </div>
  );
}

function eventPayloadDetails(event: RuntimeEvent, payload: Record<string, unknown> | null): EventPayloadDetails {
  if (!payload) return { fields: [], sections: null, additionalFields: [] };
  const consumedKeys = new Set<string>();
  const fields = eventPayloadFields(event, payload, consumedKeys);
  const sections = eventPayloadSections(event, payload, consumedKeys);
  const additionalFields = Object.entries(payload)
    .filter(([key]) => !consumedKeys.has(key))
    .map(([key, value]) => ({
      label: humanizePayloadKey(key),
      value,
      multiline: isMultilinePayloadValue(value),
    }));
  return { fields, sections, additionalFields };
}

function eventPayloadFields(
  event: RuntimeEvent,
  payload: Record<string, unknown>,
  consumedKeys: Set<string>
): PayloadField[] {
  const fields: PayloadField[] = [];
  const addKey = (label: string, key: string, options: { multiline?: boolean } = {}) => {
    if (consumedKeys.has(key) || !hasOwnPayloadKey(payload, key)) return;
    consumedKeys.add(key);
    const value = payload[key];
    if (!hasPayloadValue(value)) return;
    fields.push({
      label,
      value,
      multiline: options.multiline,
    });
  };
  const addFirst = (label: string, keys: string[], options: { multiline?: boolean } = {}) => {
    const presentKeys = keys.filter((key) => hasOwnPayloadKey(payload, key));
    for (const key of presentKeys) consumedKeys.add(key);
    const key = presentKeys.find((candidate) => hasPayloadValue(payload[candidate]));
    if (!key) return;
    fields.push({
      label,
      value: payload[key],
      multiline: options.multiline,
    });
  };

  switch (event.type) {
    case "run.created":
      addKey("Entry node", "entry_node_id");
      addKey("Graph hash", "graph_hash");
      addKey("Graph snapshot", "graph_snapshot_hash");
      addKey("Graph session", "graph_session_id");
      addKey("Source run", "source_run_id");
      addKey("Source checkpoint", "source_checkpoint_id");
      break;
    case "run.started":
      addKey("Fork request", "fork_request_key");
      break;
    case "run.forked":
      addKey("Source run", "source_run_id");
      addKey("Source checkpoint", "source_checkpoint_id");
      addKey("Request key", "request_key");
      break;
    case "run.resumed":
      addKey("Checkpoint", "checkpoint_id");
      addKey("Node", "node_id");
      addKey("Nodes", "node_ids");
      break;
    case "run.paused":
      addKey("Checkpoint", "checkpoint_id");
      addKey("Stage", "stage");
      addKey("Node", "node_id");
      addKey("Message", "message", { multiline: true });
      break;
    case "run.failed":
      addKey("Error code", "error_code");
      addKey("Error", "error_message", { multiline: true });
      break;
    case "run.limit_exceeded":
      addKey("Limit kind", "kind");
      addKey("Limit", "limit");
      addKey("Actual", "actual");
      addKey("Error class", "error_class");
      addKey("Error", "error", { multiline: true });
      break;
    case "run.backpressure":
      addKey("Scope", "scope");
      addKey("Limit", "limit");
      break;
    case "nodes.started":
      addKey("Node name", "node_name");
      break;
    case "nodes.finished":
      addKey("Attempt", "attempt");
      break;
    case "nodes.failed":
      addKey("Attempt", "attempt");
      break;
    case "nodes.canceled":
      addKey("Attempt", "attempt");
      addKey("Error code", "error_code");
      addKey("Message", "message", { multiline: true });
      break;
    case "nodes.retry":
      addKey("Task", "task_id");
      addKey("Attempt", "attempt");
      addKey("Next attempt", "next_attempt");
      addKey("Delay", "delay");
      break;
    case "condition.failed":
      addKey("Condition", "condition_id");
      addKey("Condition type", "condition_type");
      addKey("Source node", "source_node_id");
      addKey("Target node", "target_node_id");
      addKey("State paths", "state_paths");
      break;
    case "condition.evaluated":
      addKey("Matched", "matched");
      addKey("Targets", "targets");
      addKey("Reason", "reason", { multiline: true });
      break;
    case "failure.routed":
      addKey("Source task", "source_task_id");
      addKey("Source node", "source_node_id");
      addKey("Next nodes", "next_node_ids");
      addKey("Stage", "stage");
      break;
    case "llm.call":
    case "llm.usage":
      addKey("Call", "call_id");
      addKey("Model ID", "model_id");
      addKey("Model", "model");
      addKey("Stop reason", "stop_reason");
      addKey("Calls", "calls");
      addKey("Total tokens", "total_tokens");
      addFirst("Prompt tokens", ["prompt_tokens", "input_tokens"]);
      addFirst("Completion tokens", ["completion_tokens", "output_tokens"]);
      addKey("Reasoning tokens", "reasoning_tokens");
      addKey("Cached prompt", "prompt_cached_tokens");
      addKey("Cost total", "cost_total");
      addKey("Cost currency", "cost_currency");
      break;
    case "llm.function_call":
      addKey("Call", "call_id");
      addKey("Tool call", "tool_call_id");
      addFirst("Name", ["name", "function_name"]);
      break;
    case "llm.content":
    case "llm.content_chunk":
    case "llm.reasoning":
    case "llm.reasoning_chunk":
      addKey("Call", "call_id");
      break;
    case "tool.started":
    case "tool.called":
    case "tool.approval_needed":
    case "tool.approved":
    case "tool.denied":
    case "tool.returned":
    case "tool.failed":
      addKey("Tool", "name");
      addKey("Tool call", "tool_call_id");
      addKey("Permissions", "permissions");
      addKey("Approval mode", "approval_mode");
      addKey("Count", "count");
      addKey("Parallel", "parallel");
      addKey("Is error", "is_error");
      break;
    case "subgraph.started":
      addKey("Graph ref", "graph_ref");
      addKey("Parent run", "parent_run_id");
      addKey("Parent step", "parent_step_id");
      addKey("Parent task", "parent_task_id");
      addKey("Namespace", "namespace");
      break;
    case "subgraph.finished":
      addKey("Graph ref", "graph_ref");
      addKey("Child run", "child_run_id");
      addKey("Namespace", "namespace");
      break;
    case "subgraph.failed":
      addKey("Graph ref", "graph_ref");
      addKey("Parent run", "parent_run_id");
      break;
    case "effect.intent":
    case "effect.outcome":
      addKey("Operation", "key");
      addKey("Parent operation", "parent_key");
      addKey("Kind", "kind");
      addKey("Name", "name");
      addKey("Effect class", "class");
      addKey("Effect status", "status");
      addKey("Attempt", "attempt");
      addKey("Idempotency key", "idempotency_key");
      addKey("Provider request", "provider_request_id");
      break;
    case "effect.resolution_requested":
    case "effect.resolution_outcome":
      addKey("Resolution", "id");
      addKey("Attempt", "attempt_id");
      addKey("Action", "action");
      addKey("Status", "status");
      addKey("Actor", "actor");
      addKey("Reason", "reason", { multiline: true });
      addKey("Compensation key", "compensation_key");
      addKey("Requested at", "requested_at");
      addKey("Resolved at", "resolved_at");
      break;
    case "checkpoint.created":
      addKey("Checkpoint", "checkpoint_id");
      addKey("Stage", "stage");
      break;
    case "artifact.created":
      addFirst("Artifact", ["artifact_id", "id"]);
      addKey("Transaction", "transaction_id");
      addKey("Type", "type");
      addKey("MIME", "mime_type");
      addKey("Location", "location", { multiline: true });
      break;
    case "breakpoint.hit":
      addKey("Breakpoint", "breakpoint_id");
      addKey("Stage", "stage");
      addKey("Node", "node_id");
      addKey("Hit at", "hit_at");
      break;
    case "state.changed":
      if (hasOwnPayloadKey(payload, "changes")) {
        consumedKeys.add("changes");
        const changes = payloadArray(payload.changes);
        if (changes) fields.push({ label: "Changes", value: String(changes.length) });
      }
      break;
    case "contract.violation":
      if (hasOwnPayloadKey(payload, "violations")) {
        consumedKeys.add("violations");
        const violations = payloadArray(payload.violations);
        if (violations) fields.push({ label: "Violations", value: String(violations.length) });
      }
      break;
    case "warning":
      addKey("Code", "code");
      addFirst("Node", ["node_id", "node"]);
      addKey("Message", "message", { multiline: true });
      addKey("Path", "path");
      addFirst("Iteration", ["iteration", "agent_iteration"]);
      break;
    case "nodes.custom":
      addKey("Kind", "kind");
      addKey("Event", "event");
      addKey("Provider", "provider");
      addKey("Message", "message", { multiline: true });
      addKey("Detail", "detail", { multiline: true });
      addKey("Next worker", "next_worker");
      addKey("Worker", "worker_id");
      addKey("Task", "task", { multiline: true });
      addKey("Reason", "reason", { multiline: true });
      addFirst("Iteration", ["iteration", "iterations", "agent_iteration"]);
      addKey("Turn", "turn_count");
      addKey("Result", "result", { multiline: true });
      addKey("Answer", "answer", { multiline: true });
      break;
  }

  addKey("Operation", "operation_key");
  addKey("Parent operation", "parent_operation_key");
  addKey("Idempotency key", "idempotency_key");
  addKey("Effect class", "effect_class");
  addKey("Effect status", "effect_status");
  addKey("Provider request", "provider_request_id");
  addKey("Duration (ms)", "duration_ms");
  addKey("Error code", "error_code");
  addKey("Error class", "error_class");
  addKey("Error", "error", { multiline: true });
  addKey("Agent invocation", "agent_invocation_id");
  addKey("Invocation kind", "agent_invocation_kind");
  addKey("Agent iteration", "agent_invocation_iteration");
  addKey("Invocation operation", "agent_invocation_operation_id");
  addKey("Invocation tool call", "agent_invocation_tool_call_id");
  addKey("Agent node", "agent_node_id");
  addKey("Agent tool", "agent_tool_name");

  return fields;
}

function eventPayloadSections(
  event: RuntimeEvent,
  payload: Record<string, unknown>,
  consumedKeys: Set<string>
): ReactNode {
  const sections: ReactNode[] = [];
  const addValueSection = (title: string, key: string) => {
    if (consumedKeys.has(key) || !hasOwnPayloadKey(payload, key) || !hasPayloadValue(payload[key])) return;
    consumedKeys.add(key);
    sections.push(<PayloadValueSection key={key} title={title} value={payload[key]} />);
  };
  const addObjectRows = (title: string, key: string) => {
    if (consumedKeys.has(key) || !hasOwnPayloadKey(payload, key)) return;
    const items = payloadArray(payload[key]);
    if (!items) return;
    consumedKeys.add(key);
    sections.push(<PayloadObjectRows key={key} title={title} items={items} />);
  };
  const addRecordRows = (title: string, key: string) => {
    if (consumedKeys.has(key) || !hasOwnPayloadKey(payload, key)) return;
    const record = payloadRecord(payload[key]);
    if (!record) return;
    consumedKeys.add(key);
    sections.push(<PayloadObjectRows key={key} title={title} items={[record]} />);
  };

  if (event.type === "llm.content" || event.type === "llm.content_chunk") addValueSection("Content", "text");
  if (event.type === "llm.reasoning" || event.type === "llm.reasoning_chunk") addValueSection("Reasoning", "text");
  if (event.type === "llm.function_call") addValueSection("Arguments", hasOwnPayloadKey(payload, "arguments") ? "arguments" : "args");
  if (event.type === "run.resumed") addObjectRows("Tasks", "tasks");
  if (event.type === "run.finished") addValueSection("Return value", "return_value");
  if (event.type === "condition.evaluated") addObjectRows("Sends", "sends");
  if (event.type === "tool.called") addObjectRows("Tools", "tools");
  if (event.type.startsWith("tool.")) {
    addValueSection("Arguments", "arguments");
    addValueSection("Content", "content");
    addValueSection("Result", "value");
  }
  if (event.type === "state.changed") {
    const changes = payloadArray(payload.changes);
    if (changes) sections.push(<StateChangeRows key="changes" changes={changes} />);
  }
  if (event.type === "contract.violation") {
    const violations = payloadArray(payload.violations);
    if (violations) sections.push(<PayloadObjectRows key="violations" title="Violations" items={violations} />);
  }
  if (event.type === "run.paused") {
    const hit = payloadRecord(payload.breakpoint_hit);
    if (hit) {
      consumedKeys.add("breakpoint_hit");
      sections.push(<PayloadObjectRows key="breakpoint-hit" title="Breakpoint hit" items={[hit]} />);
    }
  }

  addRecordRows("Details", "details");
  addRecordRows("Cost", "cost");
  addRecordRows("Approval", "approval");
  addRecordRows("Agent invocation", "agent_invocation");
  addRecordRows("Usage", "usage");

  return sections;
}

function PayloadValueSection({ title, value }: { title: string; value: unknown }) {
  const treeValue = parseJSONTreeValue(value);
  if (treeValue) {
    return (
      <DetailSection title={title}>
        <JSONTree value={treeValue} label={`${title} JSON tree`} scrollable={false} />
      </DetailSection>
    );
  }
  return <PayloadText title={title} text={typeof value === "string" ? value : stringifyJSON(value)} />;
}

function PayloadText({ title, text }: { title: string; text: string }) {
  return (
    <DetailSection title={title}>
      <pre className="max-h-72 overflow-auto whitespace-pre-wrap rounded-md border border-border bg-background p-2 text-[11px]">
        {text}
      </pre>
    </DetailSection>
  );
}

function StateChangeRows({ changes }: { changes: unknown[] }) {
  return (
    <DetailSection title="State changes">
      <div className="grid gap-1">
        {changes.map((change, index) => {
          const item = payloadRecord(change);
          if (!item) return <PayloadUnknownRow key={index} value={change} />;
          return (
            <div key={index} className="rounded border border-border bg-muted/30 p-2">
              <div className="mb-1 flex items-center gap-2">
                <span className="font-mono">{payloadString(item.path) || `change ${index + 1}`}</span>
                <span className="ml-auto text-muted-foreground">{changeKind(item)}</span>
              </div>
              {Object.prototype.hasOwnProperty.call(item, "before") ? (
                <PayloadMiniBlock label="Before" value={item.before} />
              ) : null}
              {Object.prototype.hasOwnProperty.call(item, "after") ? (
                <PayloadMiniBlock label="After" value={item.after} />
              ) : null}
            </div>
          );
        })}
      </div>
    </DetailSection>
  );
}

function PayloadObjectRows({ title, items }: { title: string; items: unknown[] }) {
  return (
    <DetailSection title={title}>
      <div className="grid gap-1">
        {items.map((item, index) => {
          const record = payloadRecord(item);
          if (!record) return <PayloadUnknownRow key={index} value={item} />;
          return (
            <div key={index} className="grid gap-1 rounded border border-border bg-muted/30 p-2">
              {Object.entries(record).map(([key, value]) => {
                const treeValue = parseJSONTreeValue(value);
                const multiline = isMultilinePayloadValue(value);
                return (
                  <div
                    key={key}
                    className={cn("grid gap-1", treeValue || multiline ? "" : "grid-cols-[120px_minmax(0,1fr)] items-center gap-2")}
                  >
                    <span className="text-muted-foreground">{humanizePayloadKey(key)}</span>
                    {treeValue ? (
                      <JSONTree value={treeValue} label={`${humanizePayloadKey(key)} JSON tree`} scrollable={false} />
                    ) : (
                      <span className={cn("min-w-0 font-mono", multiline ? "whitespace-pre-wrap break-words" : "truncate")}>
                        {formatPayloadValue(value)}
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
          );
        })}
      </div>
    </DetailSection>
  );
}

function PayloadMiniBlock({ label, value }: { label: string; value: unknown }) {
  const treeValue = parseJSONTreeValue(value);
  return (
    <div className="mt-1 grid gap-1">
      <span className="text-muted-foreground">{label}</span>
      {treeValue ? (
        <JSONTree value={treeValue} label={`${label} JSON tree`} scrollable={false} />
      ) : (
        <pre className="max-h-24 overflow-auto rounded bg-background p-2 text-[11px]">{stringifyJSON(value)}</pre>
      )}
    </div>
  );
}

function PayloadUnknownRow({ value }: { value: unknown }) {
  const treeValue = parseJSONTreeValue(value);
  if (treeValue) return <JSONTree value={treeValue} label="Payload JSON tree" scrollable={false} />;
  return (
    <pre className="max-h-32 overflow-auto rounded border border-border bg-background p-2 text-[11px]">
      {stringifyJSON(value)}
    </pre>
  );
}

function payloadRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return value as Record<string, unknown>;
}

function payloadArray(value: unknown): unknown[] | null {
  return Array.isArray(value) ? value : null;
}

function payloadString(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return "";
}

function hasOwnPayloadKey(payload: Record<string, unknown>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(payload, key);
}

function hasPayloadValue(value: unknown): boolean {
  if (value === undefined || value === null) return false;
  if (typeof value === "string") return value.trim() !== "";
  if (Array.isArray(value)) return value.length > 0;
  return true;
}

function formatPayloadValue(value: unknown): string {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) {
    if (value.every((item) => typeof item === "string" || typeof item === "number" || typeof item === "boolean")) {
      return value.map(String).join(", ");
    }
    return `${value.length} item${value.length === 1 ? "" : "s"}`;
  }
  return stringifyJSON(value);
}

function isMultilinePayloadValue(value: unknown): boolean {
  if (value !== null && typeof value === "object") return true;
  return typeof value === "string" && (value.includes("\n") || value.length > 120);
}

function humanizePayloadKey(key: string): string {
  const words = key
    .split("_")
    .filter(Boolean)
    .map((part) => part.toLowerCase())
    .join(" ");
  const label = words ? words.charAt(0).toUpperCase() + words.slice(1) : key;
  return label
    .replace(/\bid\b/gi, "ID")
    .replace(/\bllm\b/gi, "LLM")
    .replace(/\bmime\b/gi, "MIME")
    .replace(/\busd\b/gi, "USD");
}

function changeKind(change: Record<string, unknown>): string {
  const hasBefore = Object.prototype.hasOwnProperty.call(change, "before");
  const hasAfter = Object.prototype.hasOwnProperty.call(change, "after");
  if (hasBefore && hasAfter) return "updated";
  if (hasAfter) return "added";
  if (hasBefore) return "removed";
  return "changed";
}

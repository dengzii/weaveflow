import { useState } from "react";
import type { ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { cn, formatTimeMs, stringifyJSON } from "../../lib/utils";
import type { RuntimeEvent } from "../../types";
import { eventTone } from "./runStatusModel";
import { StatusText } from "./shared";

export function RunEventDetail({ event }: { event: RuntimeEvent }) {
  const payload = payloadRecord(event.payload);
  const fields = eventPayloadFields(event, payload);
  const sections = eventPayloadSections(event, payload);
  const hasPayload = event.payload !== undefined && event.payload !== null;

  return (
    <div className="grid gap-2 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <StatusText tone={eventTone(event.type)}>{event.type}</StatusText>
        <span className="font-mono text-muted-foreground">{event.node_id || event.run_id}</span>
        <span className="ml-auto text-muted-foreground">{formatTimeMs(event.timestamp)}</span>
      </div>
      <DetailRow label="Run" value={event.run_id} />
      {event.step_id ? <DetailRow label="Step" value={event.step_id} /> : null}
      {event.node_id ? <DetailRow label="Node" value={event.node_id} /> : null}
      {fields.length > 0 ? (
        <DetailSection title="Payload">
          <PayloadFields fields={fields} />
      </DetailSection>
      ) : null}
      {sections}
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
  value: ReactNode;
  multiline?: boolean;
}

function PayloadFields({ fields }: { fields: PayloadField[] }) {
  return (
    <div className="grid gap-1">
      {fields.map((field) => (
        <div
          key={field.label}
          className={cn(
            "grid gap-1 rounded border border-border bg-muted/30 px-2 py-1",
            field.multiline ? "" : "grid-cols-[120px_minmax(0,1fr)] items-center"
          )}
        >
          <span className="text-muted-foreground">{field.label}</span>
          <span className={cn("min-w-0 font-mono", field.multiline ? "whitespace-pre-wrap break-words" : "truncate")}>
            {field.value}
          </span>
        </div>
      ))}
    </div>
  );
}

function eventPayloadFields(event: RuntimeEvent, payload: Record<string, unknown> | null): PayloadField[] {
  if (!payload) return [];
  const fields: PayloadField[] = [];
  const add = (label: string, value: unknown, options: { multiline?: boolean } = {}) => {
    if (!hasPayloadValue(value)) return;
    fields.push({
      label,
      value: formatPayloadValue(value),
      multiline: options.multiline,
    });
  };

  switch (event.type) {
    case "run.created":
      add("Entry node", payload.entry_node_id);
      break;
    case "run.resumed":
      add("Checkpoint", payload.checkpoint_id);
      add("Node", payload.node_id);
      add("Nodes", payload.node_ids);
      break;
    case "run.paused":
      add("Checkpoint", payload.checkpoint_id);
      add("Stage", payload.stage);
      add("Node", payload.node_id);
      add("Message", payload.message, { multiline: true });
      break;
    case "run.failed":
      add("Error code", payload.error_code);
      add("Error", payload.error_message, { multiline: true });
      break;
    case "nodes.started":
      add("Node name", payload.node_name);
      break;
    case "nodes.finished":
    case "nodes.retry":
      add("Attempt", payload.attempt);
      break;
    case "nodes.failed":
      add("Attempt", payload.attempt);
      add("Error", payload.error, { multiline: true });
      break;
    case "llm.call":
      add("Model", payload.model);
      add("Stop reason", payload.stop_reason);
      add("Calls", payload.calls);
      add("Total tokens", payload.total_tokens);
      add("Prompt tokens", payload.prompt_tokens);
      add("Completion tokens", payload.completion_tokens);
      add("Reasoning tokens", payload.reasoning_tokens);
      add("Cached prompt", payload.prompt_cached_tokens);
      break;
    case "llm.function_call":
      add("Name", firstPayloadValue(payload, "name", "function_name"));
      add("Arguments", firstPayloadValue(payload, "arguments", "args"), { multiline: true });
      break;
    case "tool.called":
      add("Tool", payload.name);
      add("Tool call", payload.tool_call_id);
      add("Count", payload.count);
      break;
    case "tool.returned":
      add("Tool", payload.name);
      add("Tool call", payload.tool_call_id);
      break;
    case "tool.failed":
      add("Tool", payload.name);
      add("Tool call", payload.tool_call_id);
      add("Error", payload.error, { multiline: true });
      break;
    case "subgraph.started":
    case "subgraph.finished":
      add("Graph ref", payload.graph_ref);
      break;
    case "subgraph.failed":
      add("Graph ref", payload.graph_ref);
      add("Error", payload.error, { multiline: true });
      break;
    case "checkpoint.created":
      add("Checkpoint", payload.checkpoint_id);
      add("Stage", payload.stage);
      break;
    case "artifact.created":
      add("Artifact", firstPayloadValue(payload, "artifact_id", "id"));
      add("Type", payload.type);
      add("MIME", payload.mime_type);
      add("Location", payload.location, { multiline: true });
      break;
    case "breakpoint.hit":
      add("Breakpoint", payload.breakpoint_id);
      add("Stage", payload.stage);
      add("Node", payload.node_id);
      add("Hit at", payload.hit_at);
      break;
    case "state.changed":
      add("Changes", payloadArray(payload.changes)?.length);
      break;
    case "contract.violation":
      add("Violations", payloadArray(payload.violations)?.length);
      break;
    case "warning":
      add("Code", payload.code);
      add("Node", payload.node_id ?? payload.node);
      add("Message", payload.message, { multiline: true });
      add("Path", payload.path);
      add("Iteration", payload.iteration);
      break;
    case "nodes.custom":
      add("Kind", payload.kind);
      add("Event", payload.event);
      add("Message", payload.message, { multiline: true });
      add("Next worker", payload.next_worker);
      add("Worker", payload.worker_id);
      add("Task", payload.task, { multiline: true });
      add("Reason", payload.reason, { multiline: true });
      add("Turn", payload.turn_count);
      add("Result", payload.result, { multiline: true });
      add("Answer", payload.answer, { multiline: true });
      break;
  }

  return fields;
}

function eventPayloadSections(event: RuntimeEvent, payload: Record<string, unknown> | null): ReactNode {
  if (!payload) return null;
  const sections: ReactNode[] = [];
  const text = payloadString(payload.text);

  if (text && (event.type === "llm.content" || event.type === "llm.content_chunk")) {
    sections.push(<PayloadText key="content" title="Content" text={text} />);
  }
  if (text && (event.type === "llm.reasoning" || event.type === "llm.reasoning_chunk")) {
    sections.push(<PayloadText key="reasoning" title="Reasoning" text={text} />);
  }
  if (event.type === "tool.called") {
    const argumentsText = payloadString(payload.arguments);
    if (argumentsText) sections.push(<PayloadText key="arguments" title="Arguments" text={argumentsText} />);
    const tools = payloadArray(payload.tools);
    if (tools) sections.push(<PayloadObjectRows key="tools" title="Tools" items={tools} />);
  }
  if (event.type === "tool.returned") {
    const content = payloadString(payload.content);
    if (content) sections.push(<PayloadText key="content" title="Content" text={content} />);
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
    if (hit) sections.push(<PayloadObjectRows key="breakpoint-hit" title="Breakpoint hit" items={[hit]} />);
  }

  return sections;
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
              {Object.entries(record).map(([key, value]) => (
                <div key={key} className="grid grid-cols-[120px_minmax(0,1fr)] gap-2">
                  <span className="text-muted-foreground">{key}</span>
                  <span className="min-w-0 truncate font-mono">{formatPayloadValue(value)}</span>
                </div>
              ))}
            </div>
          );
        })}
      </div>
    </DetailSection>
  );
}

function PayloadMiniBlock({ label, value }: { label: string; value: unknown }) {
  return (
    <div className="mt-1 grid gap-1">
      <span className="text-muted-foreground">{label}</span>
      <pre className="max-h-24 overflow-auto rounded bg-background p-2 text-[11px]">{stringifyJSON(value)}</pre>
    </div>
  );
}

function PayloadUnknownRow({ value }: { value: unknown }) {
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

function firstPayloadValue(payload: Record<string, unknown>, ...keys: string[]): unknown {
  for (const key of keys) {
    if (hasPayloadValue(payload[key])) return payload[key];
  }
  return undefined;
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

function changeKind(change: Record<string, unknown>): string {
  const hasBefore = Object.prototype.hasOwnProperty.call(change, "before");
  const hasAfter = Object.prototype.hasOwnProperty.call(change, "after");
  if (hasBefore && hasAfter) return "updated";
  if (hasAfter) return "added";
  if (hasBefore) return "removed";
  return "changed";
}

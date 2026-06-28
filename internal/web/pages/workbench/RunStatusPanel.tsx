import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, ListTree } from "lucide-react";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { cn, formatTime, stringifyJSON } from "../../lib/utils";
import type { RunRecord, RuntimeEvent, StepRecord } from "../../types";
import { EventList } from "./EventList";
import { statusTone } from "./utils";

type ViewMode = "events" | "node" | "state";

const viewOptions: Array<{ id: ViewMode; label: string }> = [
  { id: "events", label: "Events" },
  { id: "node", label: "By Node" },
  { id: "state", label: "By State" },
];

export function RunStatusPanel({
  run,
  events,
  steps,
  onHide,
}: {
  run: RunRecord | null;
  events: RuntimeEvent[];
  steps: StepRecord[];
  onHide: () => void;
}) {
  const latestEvent = events[0] ?? null;
  const [view, setView] = useState<ViewMode>("events");

  return (
    <section className="flex h-72 min-h-0 flex-col border-t border-border bg-panel">
      <div className="flex h-11 shrink-0 items-center gap-3 border-b border-border px-4">
        <ListTree className="h-4 w-4 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-sm font-semibold">Run Status</span>
            <Badge tone={run ? statusTone(run.status) : "neutral"}>{run?.status ?? "idle"}</Badge>
            <span className="truncate font-mono text-xs text-muted-foreground">{run?.run_id ?? "no run selected"}</span>
          </div>
        </div>
        <div className="hidden items-center gap-1 rounded-md border border-border bg-background p-0.5 md:inline-flex">
          {viewOptions.map((option) => (
            <button
              key={option.id}
              type="button"
              onClick={() => setView(option.id)}
              className={cn(
                "rounded px-2 py-1 text-xs transition-colors",
                view === option.id ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-accent"
              )}
            >
              {option.label}
            </button>
          ))}
        </div>
        <div className="hidden min-w-0 items-center gap-2 text-xs text-muted-foreground md:flex">
          <span>{events.length} events</span>
          <span>{steps.length} steps</span>
          {latestEvent ? (
            <>
              <span className="truncate">{latestEvent.type}</span>
              <span>{formatTime(latestEvent.timestamp)}</span>
            </>
          ) : null}
        </div>
        <Button variant="ghost" size="icon" onClick={onHide} title="Hide run status" aria-label="Hide run status">
          <ChevronDown className="h-4 w-4" />
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-3">
        {view === "events" ? <EventList events={events} wide /> : null}
        {view === "node" ? <ByNodeView steps={steps} events={events} /> : null}
        {view === "state" ? <ByStateView steps={steps} events={events} /> : null}
      </div>
    </section>
  );
}

interface NodeBucket {
  nodeId: string;
  steps: StepRecord[];
  events: RuntimeEvent[];
  latestStatus: string;
  latestAt: string;
}

function ByNodeView({ steps, events }: { steps: StepRecord[]; events: RuntimeEvent[] }) {
  const buckets = useMemo(() => groupByNode(steps, events), [steps, events]);
  const [expanded, setExpanded] = useState<string | null>(buckets[0]?.nodeId ?? null);

  if (buckets.length === 0) {
    return <div className="text-sm text-muted-foreground">No node activity yet</div>;
  }

  return (
    <div className="grid gap-2">
      {buckets.map((bucket) => {
        const isOpen = expanded === bucket.nodeId;
        const latestStep = bucket.steps[0] ?? null;
        return (
          <div key={bucket.nodeId} className="rounded-md border border-border bg-background">
            <button
              type="button"
              onClick={() => setExpanded(isOpen ? null : bucket.nodeId)}
              className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-accent/50"
            >
              {isOpen ? (
                <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              ) : (
                <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              )}
              <Badge tone={statusTone(bucket.latestStatus)}>{bucket.latestStatus || "—"}</Badge>
              <span className="truncate font-mono text-xs">{bucket.nodeId}</span>
              {latestStep ? (
                <span className="truncate text-xs text-muted-foreground">
                  {latestStep.node_name ? `${latestStep.node_name} · ` : ""}attempt {latestStep.attempt}
                </span>
              ) : null}
              <span className="ml-auto text-xs text-muted-foreground">
                {bucket.steps.length} steps · {bucket.events.length} events
              </span>
              <span className="text-xs text-muted-foreground">{formatTime(bucket.latestAt)}</span>
            </button>
            {isOpen ? (
              <div className="border-t border-border px-3 py-2">
                {bucket.steps.length > 0 ? (
                  <div className="mb-2">
                    <div className="mb-1 text-xs font-semibold text-muted-foreground">Steps</div>
                    <div className="grid gap-1">
                      {bucket.steps.map((step) => (
                        <div
                          key={step.step_id}
                          className="flex items-center gap-2 rounded border border-border bg-muted/30 px-2 py-1 text-xs"
                        >
                          <Badge tone={statusTone(step.status)}>{step.status}</Badge>
                          <span className="font-mono text-[10px] text-muted-foreground">{step.step_id}</span>
                          <span className="text-muted-foreground">attempt {step.attempt}</span>
                          {step.error_code ? (
                            <span className="truncate text-destructive">
                              {step.error_code}: {step.error_message ?? ""}
                            </span>
                          ) : null}
                          <span className="ml-auto text-muted-foreground">
                            {formatTime(step.finished_at ?? step.updated_at ?? step.started_at)}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
                {bucket.events.length > 0 ? (
                  <div>
                    <div className="mb-1 text-xs font-semibold text-muted-foreground">Events</div>
                    <EventList events={bucket.events} wide />
                  </div>
                ) : (
                  <div className="text-xs text-muted-foreground">No events for this node</div>
                )}
              </div>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

interface StateBucket {
  status: string;
  steps: StepRecord[];
  events: RuntimeEvent[];
}

function ByStateView({ steps, events }: { steps: StepRecord[]; events: RuntimeEvent[] }) {
  const buckets = useMemo(() => groupByState(steps, events), [steps, events]);
  const [expanded, setExpanded] = useState<string | null>(buckets[0]?.status ?? null);

  if (buckets.length === 0) {
    return <div className="text-sm text-muted-foreground">No step or event activity yet</div>;
  }

  return (
    <div className="grid gap-2">
      {buckets.map((bucket) => {
        const isOpen = expanded === bucket.status;
        return (
          <div key={bucket.status} className="rounded-md border border-border bg-background">
            <button
              type="button"
              onClick={() => setExpanded(isOpen ? null : bucket.status)}
              className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-accent/50"
            >
              {isOpen ? (
                <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              ) : (
                <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              )}
              <Badge tone={statusTone(bucket.status)}>{bucket.status}</Badge>
              <span className="ml-auto text-xs text-muted-foreground">
                {bucket.steps.length} steps · {bucket.events.length} events
              </span>
            </button>
            {isOpen ? (
              <div className="grid gap-2 border-t border-border px-3 py-2">
                {bucket.steps.length > 0 ? (
                  <div>
                    <div className="mb-1 text-xs font-semibold text-muted-foreground">Steps</div>
                    <div className="grid gap-1">
                      {bucket.steps.map((step) => (
                        <div
                          key={step.step_id}
                          className="flex items-center gap-2 rounded border border-border bg-muted/30 px-2 py-1 text-xs"
                        >
                          <span className="truncate font-mono">{step.node_id}</span>
                          <span className="truncate text-muted-foreground">{step.node_name}</span>
                          <span className="text-muted-foreground">attempt {step.attempt}</span>
                          {step.error_code ? (
                            <span className="truncate text-destructive">{step.error_code}</span>
                          ) : null}
                          <span className="ml-auto text-muted-foreground">
                            {formatTime(step.finished_at ?? step.updated_at ?? step.started_at)}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
                {bucket.events.length > 0 ? (
                  <div>
                    <div className="mb-1 text-xs font-semibold text-muted-foreground">Events</div>
                    <div className="grid gap-1">
                      {bucket.events.map((event, idx) => (
                        <div
                          key={`${event.id}-${idx}`}
                          className="rounded border border-border bg-muted/30 p-2 text-xs"
                        >
                          <div className="flex min-w-0 items-center gap-2">
                            <span className="truncate font-mono">{event.type}</span>
                            <span className="truncate text-muted-foreground">{event.node_id || event.run_id}</span>
                            <span className="ml-auto text-muted-foreground">{formatTime(event.timestamp)}</span>
                          </div>
                          {event.payload ? (
                            <pre className="mt-1 max-h-32 overflow-auto rounded bg-background p-2 text-[11px]">
                              {stringifyJSON(event.payload)}
                            </pre>
                          ) : null}
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
              </div>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

function groupByNode(steps: StepRecord[], events: RuntimeEvent[]): NodeBucket[] {
  const map = new Map<string, NodeBucket>();
  const ensure = (nodeId: string): NodeBucket => {
    let bucket = map.get(nodeId);
    if (!bucket) {
      bucket = { nodeId, steps: [], events: [], latestStatus: "", latestAt: "" };
      map.set(nodeId, bucket);
    }
    return bucket;
  };

  for (const step of steps) {
    if (!step.node_id) continue;
    ensure(step.node_id).steps.push(step);
  }
  for (const event of events) {
    if (!event.node_id) continue;
    ensure(event.node_id).events.push(event);
  }

  for (const bucket of map.values()) {
    bucket.steps.sort((a, b) => timeRank(b.updated_at ?? b.started_at) - timeRank(a.updated_at ?? a.started_at));
    bucket.events.sort((a, b) => timeRank(b.timestamp) - timeRank(a.timestamp));
    const latestStep = bucket.steps[0];
    const latestEvent = bucket.events[0];
    bucket.latestStatus = latestStep?.status ?? latestEvent?.type ?? "";
    const stepAt = latestStep?.updated_at ?? latestStep?.started_at;
    const eventAt = latestEvent?.timestamp;
    bucket.latestAt = pickLatest(stepAt, eventAt);
  }

  return [...map.values()].sort((a, b) => timeRank(b.latestAt) - timeRank(a.latestAt));
}

function groupByState(steps: StepRecord[], events: RuntimeEvent[]): StateBucket[] {
  const map = new Map<string, StateBucket>();
  const ensure = (status: string): StateBucket => {
    let bucket = map.get(status);
    if (!bucket) {
      bucket = { status, steps: [], events: [] };
      map.set(status, bucket);
    }
    return bucket;
  };

  for (const step of steps) {
    ensure(step.status || "unknown").steps.push(step);
  }
  for (const event of events) {
    ensure(eventStateLabel(event.type)).events.push(event);
  }

  for (const bucket of map.values()) {
    bucket.steps.sort((a, b) => timeRank(b.updated_at ?? b.started_at) - timeRank(a.updated_at ?? a.started_at));
    bucket.events.sort((a, b) => timeRank(b.timestamp) - timeRank(a.timestamp));
  }

  return [...map.values()].sort((a, b) => stateOrder(a.status) - stateOrder(b.status));
}

function eventStateLabel(type: string): string {
  const lower = type.toLowerCase();
  if (lower.includes("failed") || lower.includes("error")) return "failed";
  if (lower.includes("finished") || lower.includes("succeeded") || lower.includes("completed")) return "succeeded";
  if (lower.includes("canceled") || lower.includes("cancelled")) return "canceled";
  if (lower.includes("paused")) return "paused";
  if (lower.includes("started") || lower.includes("running") || lower.includes("pending")) return "running";
  return "other";
}

function stateOrder(status: string): number {
  switch (status) {
    case "running":
    case "pending":
      return 0;
    case "paused":
      return 1;
    case "failed":
      return 2;
    case "canceled":
      return 3;
    case "succeeded":
    case "finished":
    case "completed":
      return 4;
    default:
      return 5;
  }
}

function timeRank(value?: string): number {
  if (!value) return 0;
  const ts = Date.parse(value);
  return Number.isNaN(ts) ? 0 : ts;
}

function pickLatest(a?: string, b?: string): string {
  if (!a) return b ?? "";
  if (!b) return a;
  return timeRank(a) >= timeRank(b) ? a : b;
}

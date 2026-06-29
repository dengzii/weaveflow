import { useEffect, useMemo, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent, ReactNode } from "react";
import { ChevronDown, ListTree } from "lucide-react";
import { Button } from "../../components/ui/button";
import { cn, formatTime, formatTimeMs, stringifyJSON } from "../../lib/utils";
import type { RunInterrupt, RunRecord, RuntimeEvent, StepRecord } from "../../types";
import { StatusText, type StatusTone } from "./shared";
import { statusTone } from "./utils";

type ViewMode = "events" | "node" | "state";

interface ListItem {
  key: string;
  statusLabel?: string;
  statusTone?: StatusTone;
  primary: string;
  secondary?: string;
  trailing?: string;
}

const viewOptions: Array<{ id: ViewMode; label: string }> = [
  { id: "events", label: "Events" },
  { id: "node", label: "By Node" },
  { id: "state", label: "By State" },
];

const MIN_LEFT_WIDTH = 200;
const MIN_DETAIL_WIDTH = 240;
const DEFAULT_LEFT_WIDTH_RATIO = `${100 / 3}%`;
const MIN_PANEL_HEIGHT = 180;
const DEFAULT_PANEL_HEIGHT = 320;

export function RunStatusPanel({
  run,
  interrupt,
  events,
  steps,
  onHide,
}: {
  run: RunRecord | null;
  interrupt?: RunInterrupt | null;
  events: RuntimeEvent[];
  steps: StepRecord[];
  onHide: () => void;
}) {
  const [view, setView] = useState<ViewMode>("events");
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [leftWidth, setLeftWidth] = useState<number | null>(null);
  const [panelHeight, setPanelHeight] = useState(DEFAULT_PANEL_HEIGHT);
  const containerRef = useRef<HTMLDivElement | null>(null);

  const sortedEvents = useMemo(
    () => [...events].sort((a, b) => timeRank(b.timestamp) - timeRank(a.timestamp)),
    [events]
  );
  const nodeBuckets = useMemo(() => groupByNode(steps, events), [steps, events]);
  const stateBuckets = useMemo(() => groupByState(steps, events), [steps, events]);

  useEffect(() => {
    setSelectedKey(null);
  }, [view]);

  const { items, renderDetail } = useMemo<{
    items: ListItem[];
    renderDetail: (key: string) => ReactNode;
  }>(() => {
    if (view === "events") {
      return {
        items: sortedEvents.map((event, index) => ({
          key: `${event.id}-${index}`,
          statusLabel: event.type,
          statusTone: eventTone(event.type),
          primary: event.node_id || event.run_id || "—",
          trailing: formatTimeMs(event.timestamp),
        })),
        renderDetail: (key) => {
          const target = sortedEvents.find((event, index) => `${event.id}-${index}` === key);
          if (!target) return null;
          return <EventDetail event={target} />;
        },
      };
    }
    if (view === "node") {
      return {
        items: nodeBuckets.map((bucket) => ({
          key: bucket.nodeId,
          statusLabel: bucket.latestStatus || "—",
          statusTone: statusTone(bucket.latestStatus),
          primary: bucket.nodeId,
          secondary: `${bucket.steps.length} steps · ${bucket.events.length} events`,
          trailing: formatTime(bucket.latestAt),
        })),
        renderDetail: (key) => {
          const target = nodeBuckets.find((bucket) => bucket.nodeId === key);
          if (!target) return null;
          return <NodeDetail bucket={target} />;
        },
      };
    }
    return {
      items: stateBuckets.map((bucket) => ({
        key: bucket.status,
        statusLabel: bucket.status,
        statusTone: statusTone(bucket.status),
        primary: `${bucket.steps.length} steps`,
        secondary: `${bucket.events.length} events`,
      })),
      renderDetail: (key) => {
        const target = stateBuckets.find((bucket) => bucket.status === key);
        if (!target) return null;
        return <StateDetail bucket={target} />;
      },
    };
  }, [nodeBuckets, sortedEvents, stateBuckets, view]);

  const effectiveKey =
    selectedKey && items.some((item) => item.key === selectedKey) ? selectedKey : items[0]?.key ?? null;

  function startResize(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault();
    const container = containerRef.current;
    const listPane = event.currentTarget.previousElementSibling as HTMLElement | null;
    const startX = event.clientX;
    const startWidth = leftWidth ?? listPane?.getBoundingClientRect().width ?? MIN_LEFT_WIDTH;
    const containerWidth = container?.clientWidth ?? Infinity;
    const maxWidth = Math.max(MIN_LEFT_WIDTH, containerWidth - MIN_DETAIL_WIDTH);
    const onMove = (moveEvent: PointerEvent) => {
      const delta = moveEvent.clientX - startX;
      setLeftWidth(Math.max(MIN_LEFT_WIDTH, Math.min(maxWidth, startWidth + delta)));
    };
    const onUp = () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      document.body.style.cursor = "";
    };
    document.body.style.cursor = "col-resize";
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
  }

  function startResizeHeight(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault();
    const startY = event.clientY;
    const startHeight = panelHeight;
    const maxHeight = Math.max(MIN_PANEL_HEIGHT, window.innerHeight - 160);
    const onMove = (moveEvent: PointerEvent) => {
      const delta = startY - moveEvent.clientY;
      setPanelHeight(Math.max(MIN_PANEL_HEIGHT, Math.min(maxHeight, startHeight + delta)));
    };
    const onUp = () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      document.body.style.cursor = "";
    };
    document.body.style.cursor = "row-resize";
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
  }

  const latestEvent = sortedEvents[0] ?? null;

  return (
    <section
      className="flex min-h-0 flex-col bg-panel"
      style={{ height: panelHeight }}
    >
      <div
        role="separator"
        aria-orientation="horizontal"
        onPointerDown={startResizeHeight}
        className="relative h-1 shrink-0 cursor-row-resize bg-border hover:bg-primary/50"
        title="Drag to resize"
      >
        <span className="absolute inset-x-0 -top-1 -bottom-1" />
      </div>
      <div className="flex h-11 shrink-0 items-center gap-3 border-b border-border px-4">
        <ListTree className="h-4 w-4 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-sm font-semibold">Run Status</span>
            <StatusText tone={run ? statusTone(run.status) : "neutral"}>{run?.status ?? "idle"}</StatusText>
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
                view === option.id
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-accent"
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
              <span>{formatTimeMs(latestEvent.timestamp)}</span>
            </>
          ) : null}
        </div>
        <Button variant="ghost" size="icon" onClick={onHide} title="Hide run status" aria-label="Hide run status">
          <ChevronDown className="h-4 w-4" />
        </Button>
      </div>
      {interrupt ? <InterruptBanner interrupt={interrupt} /> : null}

      <div ref={containerRef} className="flex min-h-0 flex-1">
        <div
          className="min-h-0 shrink-0 overflow-auto border-r border-border"
          style={{ width: leftWidth ?? DEFAULT_LEFT_WIDTH_RATIO }}
        >
          {items.length === 0 ? (
            <div className="p-3 text-sm text-muted-foreground">No items</div>
          ) : (
            <ul className="divide-y divide-border">
              {items.map((item) => (
                <li key={item.key}>
                  <button
                    type="button"
                    onClick={() => setSelectedKey(item.key)}
                    className={cn(
                      "flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-accent/40",
                      effectiveKey === item.key && "bg-accent text-accent-foreground"
                    )}
                  >
                    {item.statusLabel ? (
                      <StatusText tone={item.statusTone ?? "neutral"} className="shrink-0">
                        {item.statusLabel}
                      </StatusText>
                    ) : null}
                    <span className="truncate font-mono">{item.primary}</span>
                    {item.secondary ? (
                      <span className="truncate text-muted-foreground">{item.secondary}</span>
                    ) : null}
                    {item.trailing ? (
                      <span className="ml-auto shrink-0 text-muted-foreground">{item.trailing}</span>
                    ) : null}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div
          role="separator"
          aria-orientation="vertical"
          onPointerDown={startResize}
          className="relative w-px shrink-0 cursor-col-resize bg-border hover:bg-primary/50"
          title="Drag to resize"
        >
          <span className="absolute inset-y-0 -left-1.5 -right-1.5" />
        </div>

        <div className="min-h-0 flex-1 overflow-auto p-3">
          {effectiveKey ? (
            renderDetail(effectiveKey)
          ) : (
            <div className="text-sm text-muted-foreground">Select an item</div>
          )}
        </div>
      </div>
    </section>
  );
}

function InterruptBanner({ interrupt }: { interrupt: RunInterrupt }) {
  return (
    <div className="flex min-h-9 shrink-0 flex-wrap items-center gap-2 border-b border-border bg-[var(--status-warn-bg)] px-4 py-2 text-xs">
      <StatusText tone="warn">interrupted</StatusText>
      <span className="truncate text-sm">{interrupt.message || "run paused"}</span>
      {interrupt.stage ? <span className="text-muted-foreground">{interrupt.stage}</span> : null}
      {interrupt.node_id ? <span className="font-mono text-muted-foreground">{interrupt.node_id}</span> : null}
      {interrupt.checkpoint_id ? (
        <span className="ml-auto truncate font-mono text-muted-foreground">{interrupt.checkpoint_id}</span>
      ) : null}
    </div>
  );
}

function EventDetail({ event }: { event: RuntimeEvent }) {
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
      {event.payload !== undefined && event.payload !== null ? (
        <pre className="overflow-auto rounded-md border border-border bg-background p-2 text-[11px]">
          {stringifyJSON(event.payload)}
        </pre>
      ) : null}
    </div>
  );
}

function NodeDetail({ bucket }: { bucket: NodeBucket }) {
  return (
    <div className="grid gap-3 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <StatusText tone={statusTone(bucket.latestStatus)}>{bucket.latestStatus || "—"}</StatusText>
        <span className="font-mono">{bucket.nodeId}</span>
        <span className="ml-auto text-muted-foreground">
          {bucket.steps.length} steps · {bucket.events.length} events
        </span>
      </div>
      {bucket.steps.length > 0 ? (
        <DetailSection title="Steps">
          <StepRows steps={bucket.steps} />
        </DetailSection>
      ) : null}
      {bucket.events.length > 0 ? (
        <DetailSection title="Events">
          <EventRows events={bucket.events} />
        </DetailSection>
      ) : null}
    </div>
  );
}

function StateDetail({ bucket }: { bucket: StateBucket }) {
  return (
    <div className="grid gap-3 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <StatusText tone={statusTone(bucket.status)}>{bucket.status}</StatusText>
        <span className="ml-auto text-muted-foreground">
          {bucket.steps.length} steps · {bucket.events.length} events
        </span>
      </div>
      {bucket.steps.length > 0 ? (
        <DetailSection title="Steps">
          <StepRows steps={bucket.steps} showNode />
        </DetailSection>
      ) : null}
      {bucket.events.length > 0 ? (
        <DetailSection title="Events">
          <EventRows events={bucket.events} />
        </DetailSection>
      ) : null}
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

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className="truncate font-mono">{value}</span>
    </div>
  );
}

function StepRows({ steps, showNode }: { steps: StepRecord[]; showNode?: boolean }) {
  return (
    <>
      {steps.map((step) => (
        <div
          key={step.step_id}
          className="flex items-center gap-2 rounded border border-border bg-muted/30 px-2 py-1 text-xs"
        >
          <StatusText tone={statusTone(step.status)}>{step.status}</StatusText>
          {showNode ? <span className="truncate font-mono">{step.node_id}</span> : null}
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
    </>
  );
}

function EventRows({ events }: { events: RuntimeEvent[] }) {
  return (
    <>
      {events.map((event, index) => (
        <div
          key={`${event.id}-${index}`}
          className="rounded border border-border bg-muted/30 p-2 text-xs"
        >
          <div className="flex min-w-0 items-center gap-2">
            <StatusText tone={eventTone(event.type)}>{event.type}</StatusText>
            <span className="truncate font-mono text-muted-foreground">{event.node_id || event.run_id}</span>
            <span className="ml-auto text-muted-foreground">{formatTimeMs(event.timestamp)}</span>
          </div>
          {event.payload ? (
            <pre className="mt-1 max-h-32 overflow-auto rounded bg-background p-2 text-[11px]">
              {stringifyJSON(event.payload)}
            </pre>
          ) : null}
        </div>
      ))}
    </>
  );
}

function eventTone(type: string): StatusTone {
  if (type.includes("failed") || type.includes("error")) return "danger";
  if (type.includes("finished") || type.includes("succeeded") || type.includes("completed")) return "ok";
  if (type.includes("paused")) return "warn";
  if (type.includes("started") || type.includes("running")) return "live";
  return "neutral";
}

interface NodeBucket {
  nodeId: string;
  steps: StepRecord[];
  events: RuntimeEvent[];
  latestStatus: string;
  latestAt: string;
}

interface StateBucket {
  status: string;
  steps: StepRecord[];
  events: RuntimeEvent[];
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

import { useEffect, useMemo, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent, ReactNode } from "react";
import { ChevronDown, Filter, ListTree, Search, X } from "lucide-react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Select } from "../../components/ui/select";
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
  const [eventFiltersOpen, setEventFiltersOpen] = useState(false);
  const [eventTypeFilter, setEventTypeFilter] = useState("");
  const [eventNodeFilter, setEventNodeFilter] = useState("");
  const [eventKeywordFilter, setEventKeywordFilter] = useState("");
  const containerRef = useRef<HTMLDivElement | null>(null);

  const sortedEvents = useMemo(
    () => [...events].sort((a, b) => timeRank(b.timestamp) - timeRank(a.timestamp)),
    [events]
  );
  const eventListItems = useMemo(
    () => sortedEvents.map((event, index) => ({ event, key: eventListKey(event, index) })),
    [sortedEvents]
  );
  const eventTypes = useMemo(() => uniqueSorted(sortedEvents.map((event) => event.type)), [sortedEvents]);
  const eventNodes = useMemo(() => uniqueSorted(sortedEvents.map((event) => event.node_id || "")), [sortedEvents]);
  const filteredEventItems = useMemo(
    () =>
      eventListItems.filter(({ event }) =>
        eventMatchesFilters(event, {
          type: eventTypeFilter,
          node: eventNodeFilter,
          keyword: eventKeywordFilter,
        })
      ),
    [eventKeywordFilter, eventListItems, eventNodeFilter, eventTypeFilter]
  );
  const activeEventFilterCount = Number(Boolean(eventTypeFilter)) + Number(Boolean(eventNodeFilter)) + Number(Boolean(eventKeywordFilter.trim()));
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
        items: filteredEventItems.map(({ event, key }) => ({
          key,
          statusLabel: event.type,
          statusTone: eventTone(event.type),
          primary: event.node_id || event.run_id || "—",
          trailing: formatTimeMs(event.timestamp),
        })),
        renderDetail: (key) => {
          const target = filteredEventItems.find((item) => item.key === key)?.event;
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
  }, [filteredEventItems, nodeBuckets, stateBuckets, view]);

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
        <div className="min-w-0 shrink-0">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-sm font-semibold">Run Status</span>
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
        <Button
          variant="ghost"
          size="icon"
          onClick={onHide}
          title="Hide run status"
          aria-label="Hide run status"
          className="ml-auto"
        >
          <ChevronDown className="h-4 w-4" />
        </Button>
      </div>
      {interrupt ? <InterruptBanner interrupt={interrupt} /> : null}

      <div ref={containerRef} className="flex min-h-0 flex-1">
        <div
          className="min-h-0 shrink-0 overflow-auto border-r border-border"
          style={{ width: leftWidth ?? DEFAULT_LEFT_WIDTH_RATIO }}
        >
          {view === "events" ? (
            <EventFilterControls
              open={eventFiltersOpen}
              type={eventTypeFilter}
              node={eventNodeFilter}
              keyword={eventKeywordFilter}
              eventTypes={eventTypes}
              nodes={eventNodes}
              activeCount={activeEventFilterCount}
              filteredCount={filteredEventItems.length}
              totalCount={sortedEvents.length}
              onOpenChange={setEventFiltersOpen}
              onTypeChange={setEventTypeFilter}
              onNodeChange={setEventNodeFilter}
              onKeywordChange={setEventKeywordFilter}
              onClear={() => {
                setEventTypeFilter("");
                setEventNodeFilter("");
                setEventKeywordFilter("");
              }}
            />
          ) : null}
          {items.length === 0 ? (
            <div className="p-3 text-sm text-muted-foreground">
              {view === "events" && sortedEvents.length > 0 ? "No matching events" : "No items"}
            </div>
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

function EventFilterControls({
  open,
  type,
  node,
  keyword,
  eventTypes,
  nodes,
  activeCount,
  filteredCount,
  totalCount,
  onOpenChange,
  onTypeChange,
  onNodeChange,
  onKeywordChange,
  onClear,
}: {
  open: boolean;
  type: string;
  node: string;
  keyword: string;
  eventTypes: string[];
  nodes: string[];
  activeCount: number;
  filteredCount: number;
  totalCount: number;
  onOpenChange: (value: boolean) => void;
  onTypeChange: (value: string) => void;
  onNodeChange: (value: string) => void;
  onKeywordChange: (value: string) => void;
  onClear: () => void;
}) {
  const menuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const closeOnPointerDown = (event: PointerEvent) => {
      if (menuRef.current?.contains(event.target as Node)) return;
      onOpenChange(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onOpenChange(false);
    };
    window.addEventListener("pointerdown", closeOnPointerDown);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("pointerdown", closeOnPointerDown);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [onOpenChange, open]);

  return (
    <div ref={menuRef} className="sticky top-0 z-20 border-b border-border bg-panel p-2">
      <div className="flex items-center gap-2">
        <Button
          variant={activeCount > 0 ? "outline" : "ghost"}
          size="sm"
          onClick={() => onOpenChange(!open)}
          title="Filter events"
          aria-expanded={open}
          className="shrink-0"
        >
          <Filter className="h-3.5 w-3.5" />
          Filter
          {activeCount > 0 ? <span className="font-mono text-[10px]">{activeCount}</span> : null}
        </Button>
        <span className="min-w-0 truncate text-xs text-muted-foreground">
          {filteredCount}/{totalCount} events
        </span>
        {activeCount > 0 ? (
          <Button variant="ghost" size="icon" onClick={onClear} title="Clear event filters" aria-label="Clear event filters" className="ml-auto h-8 w-8">
            <X className="h-3.5 w-3.5" />
          </Button>
        ) : null}
      </div>
      {open ? (
        <div className="absolute left-2 right-2 top-11 z-30 rounded-md border border-border bg-panel p-2 shadow-lg">
          <div className="flex min-w-0 items-center gap-2">
            <Select value={type} onChange={(event) => onTypeChange(event.target.value)} className="h-8 min-w-0 flex-1 text-xs">
              <option value="">All event types</option>
              {eventTypes.map((eventType) => (
                <option key={eventType} value={eventType}>
                  {eventType}
                </option>
              ))}
            </Select>
            <Select value={node} onChange={(event) => onNodeChange(event.target.value)} className="h-8 min-w-0 flex-1 text-xs">
              <option value="">All nodes</option>
              {nodes.map((nodeId) => (
                <option key={nodeId} value={nodeId}>
                  {nodeId}
                </option>
              ))}
            </Select>
            <label className="relative block min-w-0 flex-[1.2]">
              <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={keyword}
                onChange={(event) => onKeywordChange(event.target.value)}
                placeholder="Keyword"
                className="h-8 pl-7 text-xs"
              />
            </label>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function EventDetail({ event }: { event: RuntimeEvent }) {
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

function eventListKey(event: RuntimeEvent, index: number): string {
  return `${event.id || event.run_id || "event"}-${index}`;
}

function eventMatchesFilters(
  event: RuntimeEvent,
  filters: { type: string; node: string; keyword: string }
): boolean {
  if (filters.type && event.type !== filters.type) return false;
  if (filters.node && event.node_id !== filters.node) return false;
  const keyword = filters.keyword.trim().toLowerCase();
  if (!keyword) return true;
  return eventSearchText(event).includes(keyword);
}

function eventSearchText(event: RuntimeEvent): string {
  return [
    event.id,
    event.run_id,
    event.step_id,
    event.node_id,
    event.type,
    event.timestamp,
    event.payload === undefined ? "" : stringifyJSON(event.payload),
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

function uniqueSorted(values: string[]): string[] {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))].sort((a, b) => a.localeCompare(b));
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

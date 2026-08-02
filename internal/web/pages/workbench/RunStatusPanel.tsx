import { useEffect, useMemo, useRef, useState } from "react";
import type {
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
} from "react";
import {
  ChevronDown,
  ListTree,
} from "lucide-react";
import { Button } from "../../components/ui/button";
import { cn, formatTimeMs } from "../../lib/utils";
import type { RunRecord, RuntimeEvent, TriggerType } from "../../types";
import { RunEventDetail } from "./RunEventDetail";
import { RunEventFilterControls } from "./RunEventFilters";
import { RunList } from "./RunList";
import {
  COLUMN_SEPARATOR_WIDTH,
  DEFAULT_COLUMN_RATIOS,
  MIN_PANEL_HEIGHT,
  columnBoundaryPercent,
  columnGridTemplate,
  eventListKey,
  eventMatchesFilters,
  eventTone,
  readStoredEventFilters,
  readStoredPanelHeight,
  resizeRunPanelColumnRatios,
  stateHistoryEntries,
  timeRank,
  uniqueSorted,
  writeStoredEventFilters,
  writeStoredPanelHeight,
} from "./runStatusModel";
import type { ColumnRatios, EventFilterMode, StateChangeKind, StateHistoryEntry } from "./runStatusModel";
import { StatusText } from "./shared";

export { resizeRunPanelColumnRatios } from "./runStatusModel";

const COLUMN_KEYBOARD_STEP_RATIO = 0.03;
type RunPanelView = "events" | "state";

export function RunStatusPanel({
  runs,
  runTriggerTypes,
  selectedRunId,
  onSelectRun,
  onDeleteRun,
  events,
  onHide,
}: {
  runs: RunRecord[];
  runTriggerTypes?: Partial<Record<string, TriggerType>>;
  selectedRunId?: string;
  onSelectRun?: (runId: string) => void;
  onDeleteRun?: (runId: string) => void;
  events: RuntimeEvent[];
  onHide: () => void;
}) {
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [panelView, setPanelView] = useState<RunPanelView>("events");
  const [panelHeight, setPanelHeight] = useState(readStoredPanelHeight);
  const [columnRatios, setColumnRatios] = useState<ColumnRatios>(DEFAULT_COLUMN_RATIOS);
  const [eventFiltersOpen, setEventFiltersOpen] = useState(() => readStoredEventFilters().open ?? false);
  const [eventFilterMode, setEventFilterMode] = useState<EventFilterMode>(() => readStoredEventFilters().mode ?? "include");
  const [eventTypeFilters, setEventTypeFilters] = useState<string[]>(() => readStoredEventFilters().types ?? []);
  const [eventNodeFilters, setEventNodeFilters] = useState<string[]>(() => readStoredEventFilters().nodes ?? []);
  const [eventKeywordFilter, setEventKeywordFilter] = useState(() => readStoredEventFilters().keyword ?? "");
  const columnsRef = useRef<HTMLDivElement | null>(null);
  const sortedEvents = useMemo(
    () => [...events].sort((a, b) => timeRank(b.timestamp) - timeRank(a.timestamp)),
    [events]
  );
  const runOptions = useMemo(
    () => [...(runs ?? [])].sort((a, b) => timeRank(b.started_at) - timeRank(a.started_at)),
    [runs]
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
          mode: eventFilterMode,
          types: eventTypeFilters,
          nodes: eventNodeFilters,
          keyword: eventKeywordFilter,
        })
      ),
    [eventFilterMode, eventKeywordFilter, eventListItems, eventNodeFilters, eventTypeFilters]
  );
  const stateHistoryItems = useMemo(
    () =>
      stateHistoryEntries(sortedEvents).map((entry, index) => ({
        ...entry,
        key: eventListKey(entry.event, index),
      })),
    [sortedEvents]
  );
  const totalStateChanges = useMemo(
    () => stateHistoryItems.reduce((total, entry) => total + entry.changes.length, 0),
    [stateHistoryItems]
  );
  const activeEventFilterCount = eventTypeFilters.length + eventNodeFilters.length + Number(Boolean(eventKeywordFilter.trim()));

  useEffect(() => {
    setSelectedKey(null);
  }, [selectedRunId]);

  useEffect(() => {
    writeStoredEventFilters({
      open: eventFiltersOpen,
      mode: eventFilterMode,
      types: eventTypeFilters,
      nodes: eventNodeFilters,
      keyword: eventKeywordFilter,
    });
  }, [eventFilterMode, eventFiltersOpen, eventKeywordFilter, eventNodeFilters, eventTypeFilters]);

  useEffect(() => {
    const clampPanelHeight = () => {
      setPanelHeight((current) => {
        const maxHeight = Math.max(MIN_PANEL_HEIGHT, window.innerHeight - 160);
        const next = Math.max(MIN_PANEL_HEIGHT, Math.min(maxHeight, current));
        if (next !== current) writeStoredPanelHeight(next);
        return next;
      });
    };
    window.addEventListener("resize", clampPanelHeight);
    return () => window.removeEventListener("resize", clampPanelHeight);
  }, []);

  const visibleItems = panelView === "events" ? filteredEventItems : stateHistoryItems;
  const effectiveKey =
    selectedKey && visibleItems.some((item) => item.key === selectedKey)
      ? selectedKey
      : visibleItems[0]?.key ?? null;
  const selectedEvent = visibleItems.find((item) => item.key === effectiveKey)?.event ?? null;

  function startResizeHeight(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault();
    const startY = event.clientY;
    const startHeight = panelHeight;
    const maxHeight = Math.max(MIN_PANEL_HEIGHT, window.innerHeight - 160);
    const onMove = (moveEvent: PointerEvent) => {
      const delta = startY - moveEvent.clientY;
      const nextHeight = Math.max(MIN_PANEL_HEIGHT, Math.min(maxHeight, startHeight + delta));
      setPanelHeight(nextHeight);
      writeStoredPanelHeight(nextHeight);
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

  function startResizeColumns(boundary: 0 | 1, event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault();
    const container = columnsRef.current;
    if (!container) return;
    const startX = event.clientX;
    const startRatios = columnRatios;
    const availableWidth = Math.max(1, container.clientWidth - COLUMN_SEPARATOR_WIDTH * 2);
    const onMove = (moveEvent: PointerEvent) => {
      setColumnRatios(
        resizeRunPanelColumnRatios(startRatios, boundary, moveEvent.clientX - startX, availableWidth)
      );
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

  function resizeColumnsWithKeyboard(boundary: 0 | 1, event: ReactKeyboardEvent<HTMLDivElement>) {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    event.preventDefault();
    const container = columnsRef.current;
    if (!container) return;
    const availableWidth = Math.max(1, container.clientWidth - COLUMN_SEPARATOR_WIDTH * 2);
    const direction = event.key === "ArrowLeft" ? -1 : 1;
    setColumnRatios((current) =>
      resizeRunPanelColumnRatios(current, boundary, direction * availableWidth * COLUMN_KEYBOARD_STEP_RATIO, availableWidth)
    );
  }

  return (
    <section
      aria-label="Run panel"
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
            <span className="truncate text-sm font-semibold">Run</span>
          </div>
        </div>
        <div className="flex-1" />
        <Button
          variant="ghost"
          size="icon"
          onClick={onHide}
          title="Hide run panel"
          aria-label="Hide run panel"
        >
          <ChevronDown className="h-4 w-4" />
        </Button>
      </div>

      <div
        ref={columnsRef}
        className="grid min-h-0 flex-1 overflow-hidden"
        style={{ gridTemplateColumns: columnGridTemplate(columnRatios) }}
      >
        <RunList
          runs={runOptions}
          runTriggerTypes={runTriggerTypes}
          selectedRunID={selectedRunId}
          onSelectRun={onSelectRun}
          onDeleteRun={onDeleteRun}
        />

        <ColumnResizeHandle
          label="Resize Run and Event columns"
          value={columnBoundaryPercent(columnRatios, 0)}
          onPointerDown={(event) => startResizeColumns(0, event)}
          onKeyDown={(event) => resizeColumnsWithKeyboard(0, event)}
        />

        <div aria-label={panelView === "events" ? "Event list" : "State history list"} className="flex min-h-0 min-w-0 flex-col">
          <div className="flex h-9 shrink-0 items-center border-b border-border px-3">
            <div role="tablist" aria-label="Run history view" className="flex h-full items-stretch gap-3">
              <button
                type="button"
                role="tab"
                aria-selected={panelView === "events"}
                onClick={() => setPanelView("events")}
                className={cn(
                  "border-b-2 border-transparent text-xs font-semibold text-muted-foreground hover:text-foreground",
                  panelView === "events" && "border-primary text-foreground"
                )}
              >
                Event
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={panelView === "state"}
                onClick={() => setPanelView("state")}
                className={cn(
                  "flex items-center gap-1 border-b-2 border-transparent text-xs font-semibold text-muted-foreground hover:text-foreground",
                  panelView === "state" && "border-primary text-foreground"
                )}
              >
                State
              </button>
            </div>
            {panelView === "events" ? (
              <RunEventFilterControls
                open={eventFiltersOpen}
                mode={eventFilterMode}
                types={eventTypeFilters}
                selectedNodes={eventNodeFilters}
                keyword={eventKeywordFilter}
                eventTypes={eventTypes}
                nodes={eventNodes}
                activeCount={activeEventFilterCount}
                filteredCount={filteredEventItems.length}
                totalCount={sortedEvents.length}
                onOpenChange={setEventFiltersOpen}
                onModeChange={setEventFilterMode}
                onTypesChange={setEventTypeFilters}
                onNodesChange={setEventNodeFilters}
                onKeywordChange={setEventKeywordFilter}
                onClear={() => {
                  setEventFilterMode("include");
                  setEventTypeFilters([]);
                  setEventNodeFilters([]);
                  setEventKeywordFilter("");
                }}
              />
            ) : (
              <span className="ml-auto text-[11px] text-muted-foreground">
                {stateHistoryItems.length} updates · {totalStateChanges} paths
              </span>
            )}
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {panelView === "events" ? (
              <EventHistoryList
                items={filteredEventItems}
                effectiveKey={effectiveKey}
                onSelect={setSelectedKey}
                hasEvents={sortedEvents.length > 0}
              />
            ) : (
              <StateHistoryList items={stateHistoryItems} effectiveKey={effectiveKey} onSelect={setSelectedKey} />
            )}
          </div>
        </div>

        <ColumnResizeHandle
          label="Resize Event and Event Detail columns"
          value={columnBoundaryPercent(columnRatios, 1)}
          onPointerDown={(event) => startResizeColumns(1, event)}
          onKeyDown={(event) => resizeColumnsWithKeyboard(1, event)}
        />

        <div aria-label={panelView === "events" ? "Event detail" : "State change detail"} className="flex min-h-0 min-w-0 flex-col">
          <div className="flex h-9 shrink-0 items-center border-b border-border px-3">
            <span className="text-xs font-semibold">{panelView === "events" ? "Event Detail" : "State Detail"}</span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto p-3">
            {selectedEvent ? (
              <RunEventDetail event={selectedEvent} />
            ) : (
              <div className="text-sm text-muted-foreground">
                {panelView === "events" ? "Select an event" : "No state changes recorded"}
              </div>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}

function EventHistoryList({
  items,
  effectiveKey,
  onSelect,
  hasEvents,
}: {
  items: Array<{ event: RuntimeEvent; key: string }>;
  effectiveKey: string | null;
  onSelect: (key: string) => void;
  hasEvents: boolean;
}) {
  if (items.length === 0) {
    return <div className="p-3 text-sm text-muted-foreground">{hasEvents ? "No matching events" : "No run events"}</div>;
  }
  return (
    <ul className="divide-y divide-border">
      {items.map(({ event, key }) => (
        <li key={key}>
          <button
            type="button"
            onClick={() => onSelect(key)}
            className={cn(
              "grid w-full grid-cols-[minmax(0,8rem)_minmax(0,1fr)_5.75rem] items-center gap-2 px-3 py-1 text-left text-xs hover:bg-accent/40",
              effectiveKey === key && "bg-accent text-accent-foreground"
            )}
          >
            {event.type.startsWith("run.") ? (
              <span className="col-span-2 min-w-0 truncate" title={event.type}>
                <StatusText tone={eventTone(event.type)} className="max-w-full truncate align-middle">
                  {event.type}
                </StatusText>
              </span>
            ) : (
              <>
                <span className="truncate font-mono" title={event.node_id || event.run_id}>
                  {event.node_id || event.run_id || "—"}
                </span>
                <span className="min-w-0 truncate" title={event.type}>
                  <StatusText tone={eventTone(event.type)} className="max-w-full truncate align-middle">
                    {event.type}
                  </StatusText>
                </span>
              </>
            )}
            <span className="truncate text-right text-muted-foreground">{formatTimeMs(event.timestamp)}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

function StateHistoryList({
  items,
  effectiveKey,
  onSelect,
}: {
  items: Array<StateHistoryEntry & { key: string }>;
  effectiveKey: string | null;
  onSelect: (key: string) => void;
}) {
  if (items.length === 0) {
    return <div className="p-3 text-sm text-muted-foreground">No state changes recorded</div>;
  }
  return (
    <ul className="divide-y divide-border">
      {items.map(({ event, changes, key }) => (
        <li key={key}>
          <button
            type="button"
            onClick={() => onSelect(key)}
            className={cn(
              "grid w-full grid-cols-[minmax(0,8rem)_minmax(0,1fr)_5.75rem] gap-x-2 gap-y-1 px-3 py-1.5 text-left text-xs hover:bg-accent/40",
              effectiveKey === key && "bg-accent text-accent-foreground"
            )}
          >
            <span className="truncate font-mono" title={event.node_id || event.run_id}>
              {event.node_id || event.run_id || "—"}
            </span>
            <span className="min-w-0 truncate text-muted-foreground">
              {changes.length} {changes.length === 1 ? "path" : "paths"}
            </span>
            <span className="truncate text-right text-muted-foreground">{formatTimeMs(event.timestamp)}</span>
            <span className="col-span-3 flex min-w-0 flex-wrap gap-1">
              {changes.slice(0, 4).map((change, index) => (
                <span
                  key={`${change.path}-${index}`}
                  className="flex min-w-0 max-w-full items-center gap-1 rounded bg-muted/70 px-1.5 py-0.5 font-mono text-[10px]"
                  title={`${change.kind}: ${change.path}`}
                >
                  <span className={stateChangeKindClass(change.kind)}>{stateChangeKindSymbol(change.kind)}</span>
                  <span className="truncate">{change.path}</span>
                </span>
              ))}
              {changes.length > 4 ? (
                <span className="rounded bg-muted/70 px-1.5 py-0.5 text-[10px] text-muted-foreground">+{changes.length - 4}</span>
              ) : null}
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}

function stateChangeKindSymbol(kind: StateChangeKind): string {
  switch (kind) {
    case "added":
      return "+";
    case "removed":
      return "−";
    case "updated":
      return "~";
    default:
      return "•";
  }
}

function stateChangeKindClass(kind: StateChangeKind): string {
  switch (kind) {
    case "added":
      return "text-emerald-600 dark:text-emerald-300";
    case "removed":
      return "text-rose-600 dark:text-rose-300";
    case "updated":
      return "text-amber-600 dark:text-amber-300";
    default:
      return "text-muted-foreground";
  }
}

function ColumnResizeHandle({
  label,
  value,
  onPointerDown,
  onKeyDown,
}: {
  label: string;
  value: number;
  onPointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLDivElement>) => void;
}) {
  return (
    <div
      role="separator"
      aria-label={label}
      aria-orientation="vertical"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={value}
      tabIndex={0}
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      className="relative z-10 w-px cursor-col-resize bg-border outline-none hover:bg-primary/60 focus:bg-primary/60"
      title="Drag to resize columns"
    >
      <span className="absolute inset-y-0 -left-1.5 -right-1.5" />
    </div>
  );
}

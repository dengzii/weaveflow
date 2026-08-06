import { memo, startTransition, useDeferredValue, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type {
  CSSProperties,
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
  ReactNode,
} from "react";
import {
  ChevronDown,
  ChevronRight,
  ListTree,
  LoaderCircle,
} from "lucide-react";
import { Button } from "../../components/ui/button";
import { getCheckpoint } from "../../api";
import { cn, formatTimeMs, stringifyJSON } from "../../lib/utils";
import type {
  CheckpointDetail,
  CheckpointRecord,
  RunRecord,
  RuntimeEvent,
  StepRecord,
  TriggerType,
} from "../../types";
import { RunEventDetail } from "./RunEventDetail";
import { RunEventFilterControls } from "./RunEventFilters";
import { RunList } from "./RunList";
import {
  COLUMN_SEPARATOR_WIDTH,
  DEFAULT_COLUMN_RATIOS,
  EVENT_ROW_HEIGHT,
  EVENT_ROW_OVERSCAN,
  MIN_PANEL_HEIGHT,
  columnBoundaryPercent,
  columnGridTemplate,
  eventListKey,
  eventMatchesFilters,
  eventTone,
  fixedVirtualRange,
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
const MAX_CACHED_CHECKPOINTS = 6;
const snapshotJSONCache = new WeakMap<object, string>();
type RunPanelView = "events" | "state";
type StateDetailView = "diff" | "snapshot";
type EventHistoryItem = { event: RuntimeEvent; key: string };
type StateHistoryItem = StateHistoryEntry & { key: string };

export function RunStatusPanel({
  runs,
  runTriggerTypes,
  selectedRunId,
  runInspectionLoading = false,
  runActionsDisabled = false,
  onSelectRun,
  onDeleteRun,
  steps,
  checkpoints,
  events,
  hasOlderEvents = false,
  olderEventsLoading = false,
  onLoadOlderEvents,
  onHide,
}: {
  runs: RunRecord[];
  runTriggerTypes?: Partial<Record<string, TriggerType>>;
  selectedRunId?: string;
  runInspectionLoading?: boolean;
  runActionsDisabled?: boolean;
  onSelectRun?: (runId: string) => void;
  onDeleteRun?: (runId: string) => void;
  steps?: StepRecord[];
  checkpoints?: CheckpointRecord[];
  events: RuntimeEvent[];
  hasOlderEvents?: boolean;
  olderEventsLoading?: boolean;
  onLoadOlderEvents?: () => void;
  onHide: () => void;
}) {
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [panelView, setPanelView] = useState<RunPanelView>("events");
  const [stateDetailView, setStateDetailView] = useState<StateDetailView>("snapshot");
  const [checkpointDetails, setCheckpointDetails] = useState<Record<string, CheckpointDetail>>({});
  const [checkpointErrors, setCheckpointErrors] = useState<Record<string, string>>({});
  const [panelHeight, setPanelHeight] = useState(readStoredPanelHeight);
  const [columnRatios, setColumnRatios] = useState<ColumnRatios>(DEFAULT_COLUMN_RATIOS);
  const [eventFiltersOpen, setEventFiltersOpen] = useState(() => readStoredEventFilters().open ?? false);
  const [eventFilterMode, setEventFilterMode] = useState<EventFilterMode>(() => readStoredEventFilters().mode ?? "include");
  const [eventTypeFilters, setEventTypeFilters] = useState<string[]>(() => readStoredEventFilters().types ?? []);
  const [eventNodeFilters, setEventNodeFilters] = useState<string[]>(() => readStoredEventFilters().nodes ?? []);
  const [eventKeywordFilter, setEventKeywordFilter] = useState(() => readStoredEventFilters().keyword ?? "");
  const columnsRef = useRef<HTMLDivElement | null>(null);
  const checkpointRequestIDsRef = useRef<Set<string>>(new Set());
  const checkpointContextVersionRef = useRef(0);
  const runOptions = useMemo(
    () => [...(runs ?? [])].sort((a, b) => timeRank(b.started_at) - timeRank(a.started_at)),
    [runs]
  );
  const eventListItems = useMemo(
    () => events.map((event, index) => ({ event, key: eventListKey(event, index) })),
    [events]
  );
  const eventTypes = useMemo(() => uniqueSorted(events.map((event) => event.type)), [events]);
  const eventNodes = useMemo(() => uniqueSorted(events.map((event) => event.node_id || "")), [events]);
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
      panelView === "state"
        ? stateHistoryEntries(events, steps, checkpoints).map((entry, index) => ({
            ...entry,
            key: stateHistoryListKey(entry, index),
          }))
        : [],
    [checkpoints, events, panelView, steps]
  );
  const totalStateChanges = useMemo(
    () => stateHistoryItems.reduce((total, entry) => total + entry.changes.length, 0),
    [stateHistoryItems]
  );
  const activeEventFilterCount = eventTypeFilters.length + eventNodeFilters.length + Number(Boolean(eventKeywordFilter.trim()));

  useLayoutEffect(() => {
    checkpointContextVersionRef.current += 1;
    checkpointRequestIDsRef.current.clear();
    setSelectedKey(null);
    setStateDetailView("snapshot");
    setCheckpointDetails({});
    setCheckpointErrors({});
  }, [selectedRunId]);

  useEffect(() => () => {
    checkpointContextVersionRef.current += 1;
    checkpointRequestIDsRef.current.clear();
  }, []);

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
  const selectedEvent = panelView === "events"
    ? filteredEventItems.find((item) => item.key === effectiveKey)?.event ?? null
    : null;
  const selectedStateEntry = panelView === "state"
    ? stateHistoryItems.find((item) => item.key === effectiveKey) ?? null
    : null;
  const deferredStateEntry = useDeferredValue(selectedStateEntry);
  const selectedCheckpointID = selectedStateEntry?.checkpointID ?? "";
  const selectedGraphID = runs.find((run) => run.run_id === selectedRunId)?.graph_id;
  const selectedCheckpointDetail = selectedCheckpointID ? checkpointDetails[selectedCheckpointID] : undefined;
  const selectedCheckpointError = selectedCheckpointID ? checkpointErrors[selectedCheckpointID] : undefined;
  const deferredCheckpointID = deferredStateEntry?.checkpointID ?? "";
  const deferredCheckpointDetail = deferredCheckpointID ? checkpointDetails[deferredCheckpointID] : undefined;
  const deferredCheckpointError = deferredCheckpointID ? checkpointErrors[deferredCheckpointID] : undefined;

  useEffect(() => {
    if (
      panelView !== "state" ||
      !selectedCheckpointID ||
      selectedCheckpointDetail ||
      selectedCheckpointError ||
      checkpointRequestIDsRef.current.has(selectedCheckpointID)
    ) {
      return;
    }
    const contextVersion = checkpointContextVersionRef.current;
    checkpointRequestIDsRef.current.add(selectedCheckpointID);
    void getCheckpoint(selectedCheckpointID, selectedGraphID)
      .then((detail) => {
        if (checkpointContextVersionRef.current !== contextVersion) return;
        startTransition(() => {
          setCheckpointDetails((current) => cacheCheckpointDetail(current, {
            record: detail.record,
            snapshot: detail.snapshot,
          }));
        });
      })
      .catch((error: unknown) => {
        if (checkpointContextVersionRef.current !== contextVersion) return;
        const message = error instanceof Error ? error.message : String(error);
        setCheckpointErrors((current) => ({ ...current, [selectedCheckpointID]: message }));
      })
      .finally(() => {
        if (checkpointContextVersionRef.current === contextVersion) {
          checkpointRequestIDsRef.current.delete(selectedCheckpointID);
        }
      });
  }, [panelView, selectedCheckpointDetail, selectedCheckpointError, selectedCheckpointID, selectedGraphID]);

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
      className="relative z-20 isolate flex min-h-0 shrink-0 flex-col overflow-hidden bg-panel"
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
          actionsDisabled={runActionsDisabled}
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
                totalCount={events.length}
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
          <div className="min-h-0 flex-1">
            {panelView === "events" ? (
              <EventHistoryList
                runID={selectedRunId ?? ""}
                items={filteredEventItems}
                effectiveKey={effectiveKey}
                onSelect={setSelectedKey}
                hasEvents={events.length > 0}
                inspectionLoading={runInspectionLoading}
                hasOlderEvents={hasOlderEvents}
                loading={olderEventsLoading}
                onLoadOlder={onLoadOlderEvents}
              />
            ) : (
              <div className="h-full overflow-auto">
                <StateHistoryList items={stateHistoryItems} effectiveKey={effectiveKey} onSelect={setSelectedKey} />
              </div>
            )}
          </div>
        </div>

        <ColumnResizeHandle
          label="Resize Event and Event Detail columns"
          value={columnBoundaryPercent(columnRatios, 1)}
          onPointerDown={(event) => startResizeColumns(1, event)}
          onKeyDown={(event) => resizeColumnsWithKeyboard(1, event)}
        />

        <div aria-label={panelView === "events" ? "Event detail" : "State detail"} className="flex min-h-0 min-w-0 flex-col">
          <div className="flex h-9 shrink-0 items-center border-b border-border px-3">
            <span className="text-xs font-semibold">{panelView === "events" ? "Event Detail" : "State Detail"}</span>
            {panelView === "state" ? (
              <StateDetailTabs view={stateDetailView} onChange={setStateDetailView} />
            ) : null}
          </div>
          <div className="min-h-0 min-w-0 flex-1 overflow-auto p-3">
            {panelView === "events" ? (
              selectedEvent ? (
                <RunEventDetail event={selectedEvent} />
              ) : runInspectionLoading ? (
                <EmptyDetail>Loading event detail…</EmptyDetail>
              ) : (
                <EmptyDetail>Select an event</EmptyDetail>
              )
            ) : stateDetailView === "diff" ? (
              deferredStateEntry?.event
                ? <RunEventDetail event={deferredStateEntry.event} />
                : <EmptyDetail>This checkpoint has no path-level diff</EmptyDetail>
            ) : deferredStateEntry ? (
              deferredCheckpointError ? (
                <EmptyDetail>Checkpoint load failed: {deferredCheckpointError}</EmptyDetail>
              ) : deferredCheckpointDetail ? (
                <StateSnapshotDetail key={deferredCheckpointID} detail={deferredCheckpointDetail} />
              ) : deferredCheckpointID ? (
                <EmptyDetail>Loading checkpoint snapshot…</EmptyDetail>
              ) : (
                <EmptyDetail>No checkpoint is linked to this state change</EmptyDetail>
              )
            ) : (
              <EmptyDetail>No state checkpoints recorded</EmptyDetail>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}

function cacheCheckpointDetail(
  current: Record<string, CheckpointDetail>,
  detail: CheckpointDetail
): Record<string, CheckpointDetail> {
  const next = { ...current };
  delete next[detail.record.checkpoint_id];
  next[detail.record.checkpoint_id] = detail;
  const checkpointIDs = Object.keys(next);
  for (const checkpointID of checkpointIDs.slice(0, -MAX_CACHED_CHECKPOINTS)) {
    delete next[checkpointID];
  }
  return next;
}

function EventHistoryList({
  runID,
  items,
  effectiveKey,
  onSelect,
  hasEvents,
  inspectionLoading,
  hasOlderEvents,
  loading,
  onLoadOlder,
}: {
  runID: string;
  items: EventHistoryItem[];
  effectiveKey: string | null;
  onSelect: (key: string) => void;
  hasEvents: boolean;
  inspectionLoading: boolean;
  hasOlderEvents: boolean;
  loading: boolean;
  onLoadOlder?: () => void;
}) {
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(0);

  useLayoutEffect(() => {
    setScrollTop(0);
    if (viewportRef.current) viewportRef.current.scrollTop = 0;
  }, [runID]);

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport) return;
    const measure = () => setViewportHeight(viewport.clientHeight);
    measure();
    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", measure);
      return () => window.removeEventListener("resize", measure);
    }
    const observer = new ResizeObserver(measure);
    observer.observe(viewport);
    return () => observer.disconnect();
  }, []);

  const range = fixedVirtualRange(
    items.length,
    scrollTop,
    viewportHeight,
    EVENT_ROW_HEIGHT,
    EVENT_ROW_OVERSCAN
  );
  const loadControlHeight = hasOlderEvents ? 36 : 0;
  return (
    <div
      ref={viewportRef}
      data-event-history-viewport="true"
      className="h-full overflow-auto"
      onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
    >
      {items.length === 0 ? (
        <div className="p-3 text-sm text-muted-foreground">
          {inspectionLoading ? (
            <div className="flex items-center gap-2">
              <LoaderCircle className="h-3.5 w-3.5 animate-spin" />
              Loading events…
            </div>
          ) : (
            <>
              <div>{hasEvents ? "No matching events" : "No run events"}</div>
              {hasOlderEvents ? <LoadOlderEventsButton loading={loading} onLoad={onLoadOlder} /> : null}
            </>
          )}
        </div>
      ) : (
        <ul
          className="relative"
          style={{ height: items.length * EVENT_ROW_HEIGHT + loadControlHeight }}
        >
          {items.slice(range.start, range.end).map((item, index) => (
            <EventHistoryRow
              key={item.key}
              item={item}
              selected={effectiveKey === item.key}
              onSelect={onSelect}
              style={{
                position: "absolute",
                top: range.offset + index * EVENT_ROW_HEIGHT,
                height: EVENT_ROW_HEIGHT,
              }}
            />
          ))}
          {hasOlderEvents ? (
            <li
              className="absolute inset-x-0 flex h-9 items-center justify-center border-t border-border"
              style={{ top: items.length * EVENT_ROW_HEIGHT }}
            >
              <LoadOlderEventsButton loading={loading} onLoad={onLoadOlder} />
            </li>
          ) : null}
        </ul>
      )}
    </div>
  );
}

function LoadOlderEventsButton({ loading, onLoad }: { loading: boolean; onLoad?: () => void }) {
  return (
    <Button variant="ghost" size="sm" disabled={loading || !onLoad} onClick={onLoad}>
      {loading ? <LoaderCircle className="h-3.5 w-3.5 animate-spin" /> : <ChevronDown className="h-3.5 w-3.5" />}
      Load older
    </Button>
  );
}

const EventHistoryRow = memo(function EventHistoryRow({
  item,
  selected,
  onSelect,
  style,
}: {
  item: EventHistoryItem;
  selected: boolean;
  onSelect: (key: string) => void;
  style: CSSProperties;
}) {
  const { event, key } = item;
  return (
    <li className="w-full border-b border-border" style={style}>
      <button
        type="button"
        onClick={() => onSelect(key)}
        className={cn(
          "grid h-7 w-full grid-cols-[minmax(0,8rem)_minmax(0,1fr)_5.75rem] items-center gap-2 px-3 text-left text-xs hover:bg-accent/40",
          selected && "bg-accent text-accent-foreground"
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
  );
});

function StateHistoryList({
  items,
  effectiveKey,
  onSelect,
}: {
  items: StateHistoryItem[];
  effectiveKey: string | null;
  onSelect: (key: string) => void;
}) {
  if (items.length === 0) {
    return <div className="p-3 text-sm text-muted-foreground">No state checkpoints recorded</div>;
  }
  return (
    <ul className="divide-y divide-border">
      {items.map((entry) => (
        <StateHistoryRow
          key={entry.key}
          entry={entry}
          selected={effectiveKey === entry.key}
          onSelect={onSelect}
        />
      ))}
    </ul>
  );
}

const StateHistoryRow = memo(function StateHistoryRow({
  entry,
  selected,
  onSelect,
}: {
  entry: StateHistoryItem;
  selected: boolean;
  onSelect: (key: string) => void;
}) {
  return (
    <li className="[contain-intrinsic-size:auto_48px] [content-visibility:auto]">
      <button
        type="button"
        onClick={() => onSelect(entry.key)}
        className={cn(
          "grid w-full grid-cols-[minmax(0,8rem)_minmax(0,1fr)_5.75rem] gap-x-2 gap-y-1 px-3 py-1.5 text-left text-xs hover:bg-accent/40",
          selected && "bg-accent text-accent-foreground"
        )}
      >
        <span className="truncate font-mono" title={entry.nodeID || entry.checkpoint?.run_id}>
          {entry.nodeID || entry.checkpoint?.run_id || "—"}
        </span>
        <span className="min-w-0 truncate text-muted-foreground">
          {stateHistoryLabel(entry)}
        </span>
        <span className="truncate text-right text-muted-foreground">{formatTimeMs(entry.timestamp)}</span>
        <span className="col-span-3 flex min-w-0 flex-wrap gap-1">
          {entry.changes.slice(0, 4).map((change, index) => (
            <span
              key={`${change.path}-${index}`}
              className="flex min-w-0 max-w-full items-center gap-1 rounded bg-muted/70 px-1.5 py-0.5 font-mono text-[10px]"
              title={`${change.kind}: ${change.path}`}
            >
              <span className={stateChangeKindClass(change.kind)}>{stateChangeKindSymbol(change.kind)}</span>
              <span className="truncate">{change.path}</span>
            </span>
          ))}
          {entry.changes.length > 4 ? (
            <span className="rounded bg-muted/70 px-1.5 py-0.5 text-[10px] text-muted-foreground">+{entry.changes.length - 4}</span>
          ) : entry.kind !== "change" ? (
            <span className="rounded bg-muted/70 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
              {entry.checkpoint?.stage}
            </span>
          ) : null}
        </span>
      </button>
    </li>
  );
});

function stateHistoryListKey(entry: StateHistoryEntry, index: number): string {
  if (entry.checkpointID) return `checkpoint-${entry.checkpointID}-${entry.kind}`;
  if (entry.event) return eventListKey(entry.event, index);
  return `state-${entry.kind}-${entry.nodeID}-${entry.stepID}-${index}`;
}

function stateHistoryLabel(entry: StateHistoryEntry): string {
  switch (entry.kind) {
    case "baseline":
      return "Initial snapshot";
    case "barrier":
      return "Parallel merge";
    default:
      return `${entry.changes.length} ${entry.changes.length === 1 ? "path" : "paths"}`;
  }
}

function EmptyDetail({ children }: { children: ReactNode }) {
  return <div className="whitespace-pre-wrap break-words text-sm text-muted-foreground">{children}</div>;
}

export function StateDetailTabs({
  view,
  onChange,
}: {
  view: StateDetailView;
  onChange: (view: StateDetailView) => void;
}) {
  return (
    <div role="tablist" aria-label="State detail view" className="ml-auto flex h-full items-stretch gap-3">
      <button
        type="button"
        role="tab"
        aria-selected={view === "diff"}
        onClick={() => onChange("diff")}
        className={cn(
          "border-b-2 border-transparent text-[11px] font-semibold text-muted-foreground hover:text-foreground",
          view === "diff" && "border-primary text-foreground"
        )}
      >
        Diff
      </button>
      <button
        type="button"
        role="tab"
        aria-selected={view === "snapshot"}
        onClick={() => onChange("snapshot")}
        className={cn(
          "border-b-2 border-transparent text-[11px] font-semibold text-muted-foreground hover:text-foreground",
          view === "snapshot" && "border-primary text-foreground"
        )}
      >
        Snapshot
      </button>
    </div>
  );
}

export const StateSnapshotDetail = memo(function StateSnapshotDetail({ detail }: { detail: CheckpointDetail }) {
  const snapshot = recordValue(detail.snapshot);
  if (!snapshot) {
    return <EmptyDetail>Checkpoint does not contain a readable State snapshot</EmptyDetail>;
  }
  const version = typeof snapshot.version === "string" ? snapshot.version : detail.record.state_version;

  return (
    <div className="grid min-w-0 gap-3 text-xs">
      <div className="grid min-w-0 gap-1 rounded-md border border-border bg-muted/20 p-2">
        <SnapshotMetaRow label="Checkpoint" value={detail.record.checkpoint_id} />
        <SnapshotMetaRow label="Stage" value={detail.record.stage} />
        {detail.record.step_id ? <SnapshotMetaRow label="Step" value={detail.record.step_id} /> : null}
        {detail.record.node_id ? <SnapshotMetaRow label="Node" value={detail.record.node_id} /> : null}
        {version ? <SnapshotMetaRow label="Version" value={version} /> : null}
        <SnapshotMetaRow label="Created" value={formatTimeMs(detail.record.created_at)} />
      </div>
      <StateSnapshotSection title="shared" value={snapshot.shared ?? {}} defaultOpen />
      <StateSnapshotSection title="scopes" value={snapshot.scopes ?? {}} defaultOpen />
      <StateSnapshotSection title="internal" value={snapshot.internal ?? {}} />
      <StateSnapshotSection title="runtime" value={snapshot.runtime ?? {}} />
    </div>
  );
});

function SnapshotMetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid min-w-0 grid-cols-[5rem_minmax(0,1fr)] gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0 break-all font-mono">{value}</span>
    </div>
  );
}

function StateSnapshotSection({
  title,
  value,
  defaultOpen = false,
}: {
  title: string;
  value: unknown;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const [serialization, setSerialization] = useState<{ value: unknown; text: string } | null>(null);
  const serializedValue = serialization?.value === value ? serialization.text : null;

  useEffect(() => {
    if (!open || serializedValue !== null) return;
    return scheduleSnapshotSerialization(() => {
      setSerialization({ value, text: serializeSnapshotValue(value) });
    });
  }, [open, serializedValue, value]);

  return (
    <section className="min-w-0 rounded-md border border-border bg-background">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
        className="flex w-full min-w-0 items-center gap-1.5 px-2 py-1.5 text-left font-mono text-xs font-semibold hover:bg-accent/40"
      >
        <ChevronRight className={cn("h-3.5 w-3.5 shrink-0 transition-transform", open && "rotate-90")} />
        <span className="truncate">{title}</span>
      </button>
      {open ? (
        serializedValue === null ? (
          <div
            aria-label={`${title} state snapshot`}
            className="border-t border-border p-2 text-[11px] text-muted-foreground [overflow-wrap:anywhere]"
          >
            Preparing snapshot…
          </div>
        ) : (
          <pre
            aria-label={`${title} state snapshot`}
            className="min-w-0 whitespace-pre-wrap break-words border-t border-border p-2 text-[11px] [overflow-wrap:anywhere]"
          >
            {serializedValue}
          </pre>
        )
      ) : null}
    </section>
  );
}

function scheduleSnapshotSerialization(callback: () => void): () => void {
  const idleWindow = window as Window & {
    requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number;
    cancelIdleCallback?: (handle: number) => void;
  };
  if (idleWindow.requestIdleCallback && idleWindow.cancelIdleCallback) {
    const handle = idleWindow.requestIdleCallback(callback, { timeout: 100 });
    return () => idleWindow.cancelIdleCallback?.(handle);
  }
  const handle = window.setTimeout(callback, 0);
  return () => window.clearTimeout(handle);
}

function serializeSnapshotValue(value: unknown): string {
  if (value && typeof value === "object") {
    const cached = snapshotJSONCache.get(value);
    if (cached !== undefined) return cached;
    const serialized = stringifyJSON(value) ?? "null";
    snapshotJSONCache.set(value, serialized);
    return serialized;
  }
  return stringifyJSON(value) ?? "null";
}

function recordValue(value: unknown): Record<string, unknown> | null {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
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

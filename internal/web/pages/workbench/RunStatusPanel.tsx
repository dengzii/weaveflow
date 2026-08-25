import { memo, startTransition, useCallback, useDeferredValue, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type {
  CSSProperties,
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
  ReactNode,
} from "react";
import {
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  FoldVertical,
  GitBranch,
  GitCompare,
  ListTree,
  LoaderCircle,
  Search,
  UnfoldVertical,
} from "lucide-react";
import { Button } from "../../components/ui/button";
import { getCheckpoint } from "../../api";
import { cn, formatDateTimeMs, stringifyJSON } from "../../lib/utils";
import type {
  CheckpointDetail,
  CheckpointRecord,
  RunRecord,
  RunComparison,
  RuntimeEvent,
  StepRecord,
  TriggerType,
} from "../../types";
import { RunEventDetail } from "./RunEventDetail";
import { RunEventFilterControls } from "./RunEventFilters";
import { RunList } from "./RunList";
import { JSONTree } from "./JSONTree";
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
  selectRunIOCheckpoints,
  summarizeRunMetrics,
  stateHistoryEntries,
  timeRank,
  uniqueSorted,
  writeStoredEventFilters,
  writeStoredPanelHeight,
} from "./runStatusModel";
import type { ColumnRatios, EventFilterMode, RunMetricsSummary, StateChangeKind, StateHistoryEntry } from "./runStatusModel";
import { StatusText } from "./shared";

export { resizeRunPanelColumnRatios } from "./runStatusModel";

const COLUMN_KEYBOARD_STEP_RATIO = 0.03;
const MAX_CACHED_CHECKPOINTS = 6;
type RunPanelView = "overview" | "io" | "metrics" | "events" | "state" | "compare";
type StateDetailView = "diff" | "snapshot";
type EventHistoryItem = { event: RuntimeEvent; key: string };
type StateHistoryItem = StateHistoryEntry & { key: string };

export function RunStatusPanel({
  runs,
  runTriggerTypes,
  selectedRunId,
  loading = false,
  runInspectionLoading = false,
  runActionsDisabled = false,
  onSelectRun,
  onDeleteRun,
  onForkRun,
  onCompareRuns,
  runComparison,
  runComparisonLoading = false,
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
  loading?: boolean;
  runInspectionLoading?: boolean;
  runActionsDisabled?: boolean;
  onSelectRun?: (runId: string) => void;
  onDeleteRun?: (runId: string) => void;
  onForkRun?: () => void;
  onCompareRuns?: (runId: string) => void;
  runComparison?: RunComparison | null;
  runComparisonLoading?: boolean;
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
  const [compareRunID, setCompareRunID] = useState("");
  const [pendingCheckpointJump, setPendingCheckpointJump] = useState<{ runID: string; checkpointID: string } | null>(null);
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
  const inspectionLoading = loading || runInspectionLoading;
  const selectedRun = useMemo(
    () => runOptions.find((run) => run.run_id === selectedRunId),
    [runOptions, selectedRunId]
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
  const runMetrics = useMemo(
    () => summarizeRunMetrics(selectedRun, steps, checkpoints, events),
    [checkpoints, events, selectedRun, steps]
  );
  const runIOCheckpoints = useMemo(
    () => selectRunIOCheckpoints(
      checkpoints?.filter((checkpoint) => checkpoint.run_id === selectedRunId)
    ),
    [checkpoints, selectedRunId]
  );
  const forkCheckpoint = selectedRun?.last_checkpoint_id
    ? checkpoints?.find((checkpoint) => checkpoint.checkpoint_id === selectedRun.last_checkpoint_id)
    : undefined;
  const canForkSelectedRun = Boolean(
    selectedRun?.last_checkpoint_id && forkCheckpoint?.stage !== "final"
  );
  const activeEventFilterCount = eventTypeFilters.length + eventNodeFilters.length + Number(Boolean(eventKeywordFilter.trim()));
  const compareOptions = useMemo(
    () => runOptions.filter((run) => run.run_id !== selectedRunId),
    [runOptions, selectedRunId]
  );

  useLayoutEffect(() => {
    checkpointContextVersionRef.current += 1;
    checkpointRequestIDsRef.current.clear();
    setSelectedKey(null);
    setStateDetailView("snapshot");
    setCheckpointDetails({});
    setCheckpointErrors({});
    setCompareRunID("");
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

  const isSummaryView = panelView === "overview" || panelView === "io" || panelView === "metrics" || panelView === "compare";
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
  const selectedGraphID = selectedRun?.graph_id;
  const selectedCheckpointDetail = selectedCheckpointID ? checkpointDetails[selectedCheckpointID] : undefined;
  const selectedCheckpointError = selectedCheckpointID ? checkpointErrors[selectedCheckpointID] : undefined;
  const inputCheckpointID = runIOCheckpoints.input?.checkpoint_id ?? "";
  const outputCheckpointID = runIOCheckpoints.output?.checkpoint_id ?? "";
  const inputCheckpointDetail = inputCheckpointID ? checkpointDetails[inputCheckpointID] : undefined;
  const outputCheckpointDetail = outputCheckpointID ? checkpointDetails[outputCheckpointID] : undefined;
  const inputCheckpointError = inputCheckpointID ? checkpointErrors[inputCheckpointID] : undefined;
  const outputCheckpointError = outputCheckpointID ? checkpointErrors[outputCheckpointID] : undefined;
  const deferredCheckpointID = deferredStateEntry?.checkpointID ?? "";
  const deferredCheckpointDetail = deferredCheckpointID ? checkpointDetails[deferredCheckpointID] : undefined;
  const deferredCheckpointError = deferredCheckpointID ? checkpointErrors[deferredCheckpointID] : undefined;

  useEffect(() => {
    if (!pendingCheckpointJump || pendingCheckpointJump.runID !== selectedRunId || panelView !== "state") return;
    const target = stateHistoryItems.find((entry) => entry.checkpointID === pendingCheckpointJump.checkpointID);
    if (!target) return;
    setSelectedKey(target.key);
    setPendingCheckpointJump(null);
  }, [panelView, pendingCheckpointJump, selectedRunId, stateHistoryItems]);

  const jumpToCheckpoint = useCallback((runID: string, checkpointID: string) => {
    setPanelView("state");
    setPendingCheckpointJump({ runID, checkpointID });
    if (runID !== selectedRunId) {
      onSelectRun?.(runID);
    }
  }, [onSelectRun, selectedRunId]);

  useEffect(() => {
    if (!selectedGraphID || !selectedRunId) return;
    const checkpointIDs = panelView === "state"
      ? [selectedCheckpointID]
      : panelView === "io"
        ? [inputCheckpointID, outputCheckpointID]
        : [];
    const contextVersion = checkpointContextVersionRef.current;
    for (const checkpointID of [...new Set(checkpointIDs.filter(Boolean))]) {
      if (
        checkpointDetails[checkpointID] ||
        checkpointErrors[checkpointID] ||
        checkpointRequestIDsRef.current.has(checkpointID)
      ) {
        continue;
      }
      checkpointRequestIDsRef.current.add(checkpointID);
      void getCheckpoint(selectedGraphID, selectedRunId, checkpointID)
        .then((detail) => {
          if (checkpointContextVersionRef.current !== contextVersion) return;
          startTransition(() => {
            setCheckpointDetails((current) => cacheCheckpointDetail(current, detail));
          });
        })
        .catch((error: unknown) => {
          if (checkpointContextVersionRef.current !== contextVersion) return;
          const message = error instanceof Error ? error.message : String(error);
          setCheckpointErrors((current) => ({ ...current, [checkpointID]: message }));
        })
        .finally(() => {
          if (checkpointContextVersionRef.current === contextVersion) {
            checkpointRequestIDsRef.current.delete(checkpointID);
          }
        });
    }
  }, [checkpointDetails, checkpointErrors, inputCheckpointID, outputCheckpointID, panelView, selectedCheckpointID, selectedGraphID, selectedRunId]);

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
        {selectedRun && onForkRun ? (
          <Button
            variant="outline"
            size="sm"
            className="h-7 gap-1 px-2 text-xs"
            onClick={onForkRun}
            disabled={runActionsDisabled || !canForkSelectedRun}
            title={!selectedRun.last_checkpoint_id
              ? "No checkpoint available for fork"
              : forkCheckpoint?.stage === "final"
                ? "Final checkpoints cannot be forked"
                : "Fork from the selected run checkpoint"}
          >
            <GitBranch className="h-3.5 w-3.5" />
            Fork
          </Button>
        ) : null}
        {onCompareRuns && compareOptions.length > 0 ? (
          <div className="flex items-center gap-1">
            <select
              aria-label="Compare selected run with"
              value={compareRunID}
              onChange={(event) => setCompareRunID(event.target.value)}
              disabled={runActionsDisabled || runComparisonLoading}
              className="h-7 max-w-36 rounded border border-border bg-background px-2 text-[11px]"
            >
              <option value="">Compare with…</option>
              {compareOptions.map((run) => (
                <option key={run.run_id} value={run.run_id}>{run.run_id}</option>
              ))}
            </select>
            <Button
              variant="outline"
              size="sm"
              className="h-7 gap-1 px-2 text-xs"
              onClick={() => {
                if (!compareRunID) return;
                setPanelView("compare");
                onCompareRuns(compareRunID);
              }}
              disabled={runActionsDisabled || runComparisonLoading || !compareRunID}
              title="Compare selected run"
            >
              <GitCompare className="h-3.5 w-3.5" />
              Compare
            </Button>
          </div>
        ) : null}
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
          loading={loading}
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

        {isSummaryView ? (
          <div
            aria-label={panelView === "overview" ? "Run overview" : panelView === "io" ? "Run input and output" : panelView === "metrics" ? "Run metrics" : "Run comparison"}
            className="col-span-3 flex min-h-0 min-w-0 flex-col"
          >
            <div className="flex h-9 shrink-0 items-center border-b border-border px-3">
              <RunPanelTabs view={panelView} onChange={setPanelView} />
            </div>
            <div className={cn("min-h-0 flex-1 p-3", panelView === "io" ? "overflow-hidden" : "overflow-auto")}>
              {panelView === "overview" ? (
                <RunOverview
                  run={selectedRun}
                  metrics={runMetrics}
                  loading={inspectionLoading}
                />
              ) : panelView === "io" ? (
                <RunInputOutput
                  run={selectedRun}
                  inputCheckpoint={runIOCheckpoints.input}
                  outputCheckpoint={runIOCheckpoints.output}
                  inputDetail={inputCheckpointDetail}
                  outputDetail={outputCheckpointDetail}
                  inputError={inputCheckpointError}
                  outputError={outputCheckpointError}
                  loading={inspectionLoading}
                />
              ) : panelView === "compare" ? (
                <RunComparisonDetail
                  comparison={runComparison}
                  loading={runComparisonLoading}
                  onSelectCheckpoint={jumpToCheckpoint}
                />
              ) : !selectedRun ? (
                <EmptyDetail>{inspectionLoading ? "Loading run metrics…" : "Select a run"}</EmptyDetail>
              ) : (
                <RunMetrics metrics={runMetrics} loading={inspectionLoading} partial={hasOlderEvents} />
              )}
            </div>
          </div>
        ) : (
          <>
            <div aria-label={panelView === "events" ? "Event list" : "State history list"} className="flex min-h-0 min-w-0 flex-col">
              <div className="flex h-9 shrink-0 items-center border-b border-border px-3">
                <RunPanelTabs view={panelView} onChange={setPanelView} />
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
                    inspectionLoading={inspectionLoading}
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
                  ) : inspectionLoading ? (
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
          </>
        )}
      </div>
    </section>
  );
}

export function RunPanelTabs({
  view,
  onChange,
}: {
  view: RunPanelView;
  onChange: (view: RunPanelView) => void;
}) {
  const tabs: Array<{ id: RunPanelView; label: string }> = [
    { id: "overview", label: "Overview" },
    { id: "io", label: "Input / Output" },
    { id: "metrics", label: "Metrics" },
    { id: "events", label: "Event" },
    { id: "state", label: "State" },
    { id: "compare", label: "Compare" },
  ];
  return (
    <div role="tablist" aria-label="Run history view" className="flex h-full items-stretch gap-3">
      {tabs.map((tab) => (
        <button
          key={tab.id}
          type="button"
          role="tab"
          aria-selected={view === tab.id}
          onClick={() => onChange(tab.id)}
          className={cn(
            "border-b-2 border-transparent text-xs font-semibold text-muted-foreground hover:text-foreground",
            view === tab.id && "border-primary text-foreground"
          )}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}

export function RunOverview({
  run,
  metrics,
  loading = false,
}: {
  run?: RunRecord;
  metrics: RunMetricsSummary;
  loading?: boolean;
}) {
  if (!run) return <EmptyDetail>{loading ? "Loading run overview…" : "Select a run"}</EmptyDetail>;
  const currentNodes = run.current_node_ids?.length
    ? run.current_node_ids.join(", ")
    : run.current_node_id || "—";
  const nextNodes = run.next_node_ids?.length ? run.next_node_ids.join(", ") : "—";
  const origin = run.origin?.type || "direct";

  return (
    <div className="grid gap-3" data-run-overview={run.run_id}>
      <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
        <SummaryCard label="Status">
          <StatusText tone={eventTone(`run.${run.status}`)}>{run.status}</StatusText>
        </SummaryCard>
        <SummaryCard label={run.finished_at ? "Duration" : "Elapsed"} value={formatDuration(metrics.durationMs)} />
        <SummaryCard label="Steps" value={`${metrics.succeededSteps} / ${metrics.stepCount}`}/>
        <SummaryCard label="Current node" value={currentNodes} mono />
      </div>

      {run.error_code || run.error_message ? (
        <section className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-xs">
          <div className="mb-1 font-semibold text-destructive">Run failure</div>
          {run.error_code ? <div className="font-mono text-destructive">{run.error_code}</div> : null}
          {run.error_message ? <div className="mt-1 whitespace-pre-wrap break-words">{run.error_message}</div> : null}
        </section>
      ) : null}

      <div className="grid gap-3 lg:grid-cols-2">
        <OverviewSection title="Execution">
          <OverviewRow label="Run ID" value={run.run_id} />
          <OverviewRow label="Root Run" value={run.root_run_id} />
          {run.parent_run_id ? <OverviewRow label="Parent Run" value={run.parent_run_id} /> : null}
          {run.parent_step_id ? <OverviewRow label="Parent Step" value={run.parent_step_id} /> : null}
          <OverviewRow label="Namespace" value={run.namespace} />
          <OverviewRow label="Origin" value={origin} />
          <OverviewRow label="Entry node" value={run.entry_node_id} />
          <OverviewRow label="Current node" value={currentNodes} />
          <OverviewRow label="Next node" value={nextNodes} />
          {run.parallel_wave_id ? <OverviewRow label="Parallel wave" value={run.parallel_wave_id} /> : null}
          {run.last_checkpoint_id ? <OverviewRow label="Last checkpoint" value={run.last_checkpoint_id} /> : null}
        </OverviewSection>

        <OverviewSection title="Graph and time">
          <OverviewRow label="Graph ID" value={run.graph_id} />
          <OverviewRow label="Graph version" value={run.graph_version} />
          {run.graph_session_id ? <OverviewRow label="Session" value={run.graph_session_id} /> : null}
          {run.graph_snapshot_hash ? <OverviewRow label="Snapshot hash" value={run.graph_snapshot_hash} /> : null}
          <OverviewRow label="Started" value={formatDateTimeMs(run.started_at)} />
          <OverviewRow label="Updated" value={formatDateTimeMs(run.updated_at)} />
          {run.finished_at ? <OverviewRow label="Finished" value={formatDateTimeMs(run.finished_at)} /> : null}
          <OverviewRow label="Revision" value={String(run.revision)} />
          <OverviewRow label="Run path" value={run.run_path.join(" → ")} />
          {run.child_run_ids?.length ? <OverviewRow label="Child runs" value={run.child_run_ids.join(", ")} /> : null}
        </OverviewSection>
      </div>

    </div>
  );
}

export function RunComparisonDetail({
  comparison,
  loading,
  onSelectCheckpoint,
}: {
  comparison?: RunComparison | null;
  loading: boolean;
  onSelectCheckpoint?: (runID: string, checkpointID: string) => void;
}) {
  if (loading) return <EmptyDetail>Loading run comparison…</EmptyDetail>;
  if (!comparison) return <EmptyDetail>Select another run and choose Compare</EmptyDetail>;
  return (
    <div data-run-comparison="true" className="grid gap-4 text-xs">
      <div className="grid gap-2 sm:grid-cols-2">
        {[comparison.left, comparison.right].map((run, index) => {
          const checkpointID = index === 0 ? comparison.checkpoint_id : comparison.other_checkpoint_id;
          return (
            <div key={run.run_id} className="rounded border border-border bg-muted/20 p-3">
              <div className="mb-2 flex items-center justify-between gap-2">
                <span className="font-semibold">{index === 0 ? "Source" : "Compared run"}</span>
                <span className="truncate font-mono text-muted-foreground" title={run.run_id}>{run.run_id}</span>
              </div>
              <div className="text-muted-foreground">Status: <span className="text-foreground">{run.status}</span></div>
              {checkpointID ? (
                <button
                  type="button"
                  className="mt-2 truncate text-left font-mono text-primary hover:underline"
                  onClick={() => onSelectCheckpoint?.(run.run_id, checkpointID)}
                  title="Select this run to inspect its checkpoint"
                >
                  Checkpoint: {checkpointID}
                </button>
              ) : (
                <div className="mt-2 text-muted-foreground">No comparable checkpoint</div>
              )}
            </div>
          );
        })}
      </div>
      <div>
        <div className="mb-2 font-semibold">State changes ({comparison.state_changes.length})</div>
        {comparison.state_changes.length === 0 ? (
          <div className="text-muted-foreground">No state differences</div>
        ) : (
          <div className="divide-y divide-border rounded border border-border">
            {comparison.state_changes.map((change) => (
              <div key={change.path} className="grid gap-1 p-2 sm:grid-cols-[minmax(0,1fr)_1fr_1fr] sm:items-start">
                <span className="font-mono text-primary" title={change.path}>{change.path}</span>
                <span className="break-words text-muted-foreground">{valuePreview(change.before)}</span>
                <span className="break-words">{valuePreview(change.after)}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export function RunInputOutput({
  run,
  inputCheckpoint,
  outputCheckpoint,
  inputDetail,
  outputDetail,
  inputError,
  outputError,
  loading = false,
}: {
  run?: RunRecord;
  inputCheckpoint?: CheckpointRecord;
  outputCheckpoint?: CheckpointRecord;
  inputDetail?: CheckpointDetail;
  outputDetail?: CheckpointDetail;
  inputError?: string;
  outputError?: string;
  loading?: boolean;
}) {
  if (!run) return <EmptyDetail>{loading ? "Loading run input and output…" : "Select a run"}</EmptyDetail>;
  if (loading && !inputCheckpoint && !outputCheckpoint && !inputDetail && !outputDetail) {
    return <EmptyDetail>Loading run input and output…</EmptyDetail>;
  }
  const inputValue = businessSnapshot(inputDetail);
  const outputValue = runOutputDocument(run, outputDetail);

  return (
    <div className="grid h-full min-h-0 gap-3 lg:grid-cols-2" data-run-input-output={run.run_id}>
      <IODocument
        title="Input"
        checkpoint={inputDetail?.record ?? inputCheckpoint}
        value={inputValue}
        error={inputError}
        loading={Boolean(inputCheckpoint && !inputDetail && !inputError)}
        empty="No initial checkpoint was recorded"
      />
      <IODocument
        title="Output"
        checkpoint={outputDetail?.record ?? outputCheckpoint}
        value={outputValue}
        error={outputError}
        loading={Boolean(outputCheckpoint && !outputDetail && !outputError)}
        empty="No output was recorded"
      />
    </div>
  );
}

function runOutputDocument(run: RunRecord, detail?: CheckpointDetail): Record<string, unknown> | undefined {
  const output: Record<string, unknown> = {};
  if (run.return_value !== undefined) output.return_value = run.return_value;
  const state = businessSnapshot(detail);
  if (state) output.state = state;
  if (detail?.artifacts?.length) output.artifacts = detail.artifacts;
  return Object.keys(output).length > 0 ? output : undefined;
}

function IODocument({
  title,
  checkpoint,
  value,
  error,
  loading = false,
  empty,
}: {
  title: string;
  checkpoint?: CheckpointRecord;
  value?: unknown;
  error?: string;
  loading?: boolean;
  empty: string;
}) {
  const [copied, setCopied] = useState(false);
  const [view, setView] = useState<"tree" | "raw">("tree");
  const [query, setQuery] = useState("");
  const [expandAll, setExpandAll] = useState(false);
  const hasValue = value !== undefined;
  const serialized = hasValue ? stringifyJSON(value) : "";
  const matchCount = query.trim() ? jsonMatchCount(value, query) : 0;
  const treeExpanded = expandAll || Boolean(query.trim());

  async function copyValue() {
    if (!serialized || typeof navigator === "undefined" || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(serialized);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1_500);
    } catch {
      setCopied(false);
    }
  }

  return (
    <section className="flex h-full min-h-0 min-w-0 flex-col rounded-md border border-border bg-muted/20 p-3">
      <div className="mb-2 flex min-w-0 shrink-0 items-center gap-2">
        <span className="text-xs font-semibold">{title}</span>
        {checkpoint ? (
          <span className="ml-auto text-[11px] tabular-nums text-muted-foreground">
            {formatDateTimeMs(checkpoint.created_at)}
          </span>
        ) : null}
        {hasValue ? (
          <Button variant="ghost" size="sm" className={cn("h-7 px-2 text-xs", !checkpoint && "ml-auto")} onClick={() => void copyValue()}>
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            {copied ? "Copied" : "Copy"}
          </Button>
        ) : null}
      </div>
      {hasValue ? (
        <div className="mb-2 flex shrink-0 flex-wrap items-center gap-2">
          <label className="relative min-w-40 flex-1">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              aria-label={`Search ${title.toLowerCase()} JSON`}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search JSON"
              className="h-8 w-full rounded-md border border-input bg-background pl-7 pr-16 text-xs outline-none focus:border-ring"
            />
            {query.trim() ? (
              <span className="absolute right-2 top-1/2 -translate-y-1/2 text-[10px] tabular-nums text-muted-foreground">
                {matchCount} {matchCount === 1 ? "match" : "matches"}
              </span>
            ) : null}
          </label>
          <div className="ml-auto flex items-center gap-1.5">
            <div role="tablist" aria-label={`${title} JSON view`} className="flex rounded border border-border bg-background p-0.5">
              {(["tree", "raw"] as const).map((option) => (
                <button
                  key={option}
                  type="button"
                  role="tab"
                  aria-selected={view === option}
                  onClick={() => setView(option)}
                  className={cn(
                    "rounded px-2 py-1 text-[11px] capitalize text-muted-foreground hover:text-foreground",
                    view === option && "bg-accent text-foreground"
                  )}
                >
                  {option}
                </button>
              ))}
            </div>
            {view === "tree" ? (
              <Button
                variant="ghost"
                size="icon"
                className="h-8 w-8"
                title={treeExpanded ? "Collapse all" : "Expand all"}
                aria-label={treeExpanded ? `Collapse all ${title.toLowerCase()} nodes` : `Expand all ${title.toLowerCase()} nodes`}
                onClick={() => {
                  if (treeExpanded) {
                    setExpandAll(false);
                    setQuery("");
                  } else {
                    setExpandAll(true);
                  }
                }}
              >
                {treeExpanded ? <FoldVertical className="h-4 w-4" /> : <UnfoldVertical className="h-4 w-4" />}
              </Button>
            ) : null}
          </div>
        </div>
      ) : null}
      <div className="min-h-0 flex-1 overflow-hidden">
        {error ? (
          <div className="h-full overflow-auto rounded border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            Checkpoint load failed: {error}
          </div>
        ) : loading ? (
          <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
            <LoaderCircle className="h-3.5 w-3.5 animate-spin" />
            Loading checkpoint…
          </div>
        ) : hasValue ? (
          view === "tree" ? (
            <JSONTree value={value} query={query} label={`${title} JSON tree`} expandAll={expandAll} />
          ) : (
            <pre
              aria-label={`${title} raw JSON`}
              className="h-full min-w-0 overflow-auto whitespace-pre-wrap break-words rounded border border-border bg-background p-2 text-[11px] [overflow-wrap:anywhere]"
            >
              {serialized}
            </pre>
          )
        ) : (
          <div className="flex h-full items-center justify-center text-xs text-muted-foreground">{empty}</div>
        )}
      </div>
    </section>
  );
}

function jsonMatchCount(value: unknown, query: string): number {
  const normalizedQuery = query.trim().toLowerCase();
  if (!normalizedQuery) return 0;
  if (value === null || value === undefined) return String(value).includes(normalizedQuery) ? 1 : 0;
  if (typeof value !== "object") return String(value).toLowerCase().includes(normalizedQuery) ? 1 : 0;
  const entries = Array.isArray(value)
    ? value.map((item, index) => [String(index), item] as const)
    : Object.entries(value as Record<string, unknown>);
  return entries.reduce(
    (total, [key, item]) => total + Number(key.toLowerCase().includes(normalizedQuery)) + jsonMatchCount(item, normalizedQuery),
    0
  );
}

export function RunMetrics({
  metrics,
  loading = false,
  partial = false,
}: {
  metrics: RunMetricsSummary;
  loading?: boolean;
  partial?: boolean;
}) {
  if (loading && metrics.eventCount === 0 && metrics.stepCount === 0) {
    return <EmptyDetail>Loading run metrics…</EmptyDetail>;
  }
  const totalTokens = metrics.promptTokens + metrics.completionTokens + metrics.reasoningTokens;
  return (
    <div className="grid gap-3" data-run-metrics="true">
      {partial ? (
        <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200">
          Event-derived metrics include loaded events only. Load older events in the Event tab for complete totals.
        </div>
      ) : null}
      <section>
        <MetricSectionTitle>Execution</MetricSectionTitle>
        <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
          <SummaryCard label="Duration" value={formatDuration(metrics.durationMs)} />
          <SummaryCard label="Events" value={formatMetric(metrics.eventCount)} />
          <SummaryCard label="Steps" value={formatMetric(metrics.stepCount)} />
          <SummaryCard label="Checkpoints" value={formatMetric(metrics.checkpointCount)} />
          <SummaryCard label="Succeeded" value={formatMetric(metrics.succeededSteps)} />
          <SummaryCard label="Active" value={formatMetric(metrics.activeSteps)} />
          <SummaryCard label="Failed" value={formatMetric(metrics.failedSteps)} />
          <SummaryCard label="Retries" value={formatMetric(metrics.retries)} />
        </div>
      </section>
      <section>
        <MetricSectionTitle>Model usage</MetricSectionTitle>
        <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
          <SummaryCard label="LLM calls" value={formatMetric(metrics.llmCallCount)} />
          <SummaryCard label="Total tokens" value={formatMetric(totalTokens)} />
          <SummaryCard label="Prompt tokens" value={formatMetric(metrics.promptTokens)} />
          <SummaryCard label="Completion tokens" value={formatMetric(metrics.completionTokens)} />
          <SummaryCard label="Reasoning tokens" value={formatMetric(metrics.reasoningTokens)} />
          <SummaryCard label="Cached prompt" value={formatMetric(metrics.cachedPromptTokens)} />
        </div>
      </section>
      <section>
        <MetricSectionTitle>Tools and state</MetricSectionTitle>
        <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
          <SummaryCard label="Tool calls" value={formatMetric(metrics.toolCallCount)} />
          <SummaryCard label="Tool failures" value={formatMetric(metrics.toolFailureCount)} />
          <SummaryCard label="State changes" value={formatMetric(metrics.stateChangeCount)} />
          <SummaryCard label="Warnings" value={formatMetric(metrics.warningCount)} />
          <SummaryCard label="Error events" value={formatMetric(metrics.errorCount)} />
        </div>
      </section>
    </div>
  );
}

function SummaryCard({
  label,
  value,
  hint,
  mono = false,
  children,
}: {
  label: string;
  value?: string;
  hint?: string;
  mono?: boolean;
  children?: ReactNode;
}) {
  return (
    <div className="min-w-0 rounded-md border border-border bg-muted/30 p-3">
      <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className={cn("mt-1 truncate text-sm font-semibold tabular-nums", mono && "font-mono")} title={value}>
        {children ?? value ?? "—"}
      </div>
      {hint ? <div className="mt-0.5 text-[10px] text-muted-foreground">{hint}</div> : null}
    </div>
  );
}

function OverviewSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="rounded-md border border-border bg-muted/20 p-3">
      <div className="mb-2 text-xs font-semibold">{title}</div>
      <div className="grid gap-1.5">{children}</div>
    </section>
  );
}

function OverviewRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[110px_minmax(0,1fr)] gap-2 text-xs">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0 truncate font-mono" title={value}>{value || "—"}</span>
    </div>
  );
}

function MetricSectionTitle({ children }: { children: ReactNode }) {
  return <div className="mb-2 text-xs font-semibold">{children}</div>;
}

function formatMetric(value: number): string {
  return new Intl.NumberFormat("en-US").format(value);
}

function businessSnapshot(detail?: CheckpointDetail): Record<string, unknown> | undefined {
  const state = recordValue(detail?.business) ?? recordValue(detail?.snapshot);
  if (!state) return undefined;
  return {
    shared: state.shared ?? {},
    scopes: state.scopes ?? {},
  };
}

function valuePreview(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "string") return value.length > 120 ? `${value.slice(0, 117)}…` : value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return `Array(${value.length})`;
  const record = recordValue(value);
  if (record) return `Object(${Object.keys(record).length})`;
  return typeof value;
}

function formatDuration(durationMs: number): string {
  if (!Number.isFinite(durationMs) || durationMs <= 0) return "0 ms";
  if (durationMs < 1_000) return `${Math.round(durationMs)} ms`;
  const seconds = durationMs / 1_000;
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)} s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = Math.floor(seconds % 60);
  if (minutes < 60) return `${minutes}m ${remainingSeconds}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
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
  const toolName = eventToolName(event);
  return (
    <li className="w-full border-b border-border" style={style}>
      <button
        type="button"
        onClick={() => onSelect(key)}
        className={cn(
          "grid h-7 w-full items-center gap-2 px-3 text-left text-xs hover:bg-accent/40",
          event.type === "tool.called"
            ? "grid-cols-[minmax(0,8rem)_minmax(0,1fr)_minmax(0,8rem)_8rem]"
            : "grid-cols-[minmax(0,8rem)_minmax(0,1fr)_8rem]",
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
        {event.type === "tool.called" ? (
          <span
            className="min-w-0 truncate font-mono"
            title={toolName || "Tool name unavailable"}
            aria-label={`Tool name: ${toolName || "unavailable"}`}
          >
            {toolName || "—"}
          </span>
        ) : null}
        <span className="truncate text-right tabular-nums text-muted-foreground" title={event.timestamp}>
          {formatDateTimeMs(event.timestamp)}
        </span>
      </button>
    </li>
  );
});

function eventToolName(event: RuntimeEvent): string {
  if (event.type !== "tool.called" || !event.payload || typeof event.payload !== "object" || Array.isArray(event.payload)) {
    return "";
  }
  const payload = event.payload as Record<string, unknown>;
  const names = typeof payload.name === "string" ? [payload.name] : [];
  if (Array.isArray(payload.tools)) {
    for (const tool of payload.tools) {
      if (!tool || typeof tool !== "object" || Array.isArray(tool)) continue;
      const name = (tool as Record<string, unknown>).name;
      if (typeof name === "string") names.push(name);
    }
  }
  return [...new Set(names.map((name) => name.trim()).filter(Boolean))].join(", ");
}

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
          "grid w-full grid-cols-[minmax(0,8rem)_minmax(0,1fr)_8rem] gap-x-2 gap-y-1 px-3 py-1.5 text-left text-xs hover:bg-accent/40",
          selected && "bg-accent text-accent-foreground"
        )}
      >
        <span className="truncate font-mono" title={entry.nodeID || entry.checkpoint?.run_id}>
          {entry.nodeID || entry.checkpoint?.run_id || "—"}
        </span>
        <span className="min-w-0 truncate text-muted-foreground">
          {stateHistoryLabel(entry)}
        </span>
        <span className="truncate text-right tabular-nums text-muted-foreground" title={entry.timestamp}>
          {formatDateTimeMs(entry.timestamp)}
        </span>
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
        <SnapshotMetaRow label="Created" value={formatDateTimeMs(detail.record.created_at)} />
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
        <div className="border-t border-border p-2">
          <JSONTree value={value} query="" label={`${title} state snapshot`} expandAll={false} />
        </div>
      ) : null}
    </section>
  );
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

import { forwardRef, useEffect, useMemo, useRef, useState } from "react";
import type {
  CSSProperties,
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
  ReactNode,
} from "react";
import { createPortal } from "react-dom";
import {
  Ban,
  Check,
  ChevronDown,
  Circle,
  Clock3,
  Filter,
  ListTree,
  Loader2,
  MessageCircle,
  Pause,
  Play,
  Search,
  Trash2,
  Webhook,
  X,
} from "lucide-react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { cn, formatTime, formatTimeMs, stringifyJSON } from "../../lib/utils";
import type { RunRecord, RuntimeEvent, TriggerType } from "../../types";
import { StatusText, type StatusTone } from "./shared";

type EventFilterMode = "include" | "exclude";
type ColumnRatios = [number, number, number];

interface EventFilterPopoverPosition {
  anchor: "above" | "below";
  left: number;
  width: number;
  maxHeight: number;
  bodyMaxHeight: number;
  top?: number;
  bottom?: number;
}

const MIN_PANEL_HEIGHT = 180;
const DEFAULT_PANEL_HEIGHT = 320;
const DEFAULT_COLUMN_RATIOS: ColumnRatios = [1, 1.5, 2];
const MIN_COLUMN_WIDTHS: ColumnRatios = [180, 260, 280];
const COLUMN_SEPARATOR_WIDTH = 1;
const COLUMN_KEYBOARD_STEP_RATIO = 0.03;
const FILTER_POPOVER_GAP = 4;
const FILTER_POPOVER_MARGIN = 8;
const FILTER_POPOVER_MIN_WIDTH = 320;
const FILTER_POPOVER_MAX_WIDTH = 520;
const FILTER_POPOVER_MAX_HEIGHT = 384;
const FILTER_POPOVER_HEADER_HEIGHT = 45;
const FILTER_POPOVER_MIN_AVAILABLE_HEIGHT = 220;
const EVENT_FILTER_STORAGE_KEY = "weaveflow.workbench.runStatus.eventFilters";
const PANEL_HEIGHT_STORAGE_KEY = "weaveflow.workbench.runStatus.height";

interface StoredEventFilters {
  open?: boolean;
  mode?: EventFilterMode;
  types?: string[];
  nodes?: string[];
  keyword?: string;
}

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

  const effectiveKey =
    selectedKey && filteredEventItems.some((item) => item.key === selectedKey)
      ? selectedKey
      : filteredEventItems[0]?.key ?? null;
  const selectedEvent = filteredEventItems.find((item) => item.key === effectiveKey)?.event ?? null;

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
        <div aria-label="Run list" className="flex min-h-0 min-w-0 flex-col">
          <div className="flex h-9 shrink-0 items-center border-b border-border px-3">
            <span className="text-xs font-semibold">Run</span>
            <span className="ml-auto text-xs text-muted-foreground">{runOptions.length}</span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {runOptions.length === 0 ? (
              <div className="p-3 text-sm text-muted-foreground">No runs</div>
            ) : (
              <ul className="divide-y divide-border">
                {runOptions.map((option) => {
                  const active = option.run_id === selectedRunId;
                  const canDelete = Boolean(onDeleteRun) && !isRunActive(option.status);
                  const triggerType = runTriggerTypes?.[option.run_id];
                  return (
                    <li
                      key={option.run_id}
                      className={cn(
                        "grid grid-cols-[minmax(0,1fr)_2rem]",
                        active && "bg-accent text-accent-foreground"
                      )}
                    >
                      <button
                        type="button"
                        onClick={() => onSelectRun?.(option.run_id)}
                        aria-pressed={active}
                        className="min-w-0 px-3 py-1.5 text-left text-xs hover:bg-accent/40"
                      >
                        <div className="flex min-w-0 items-center gap-2">
                          <RunSourceIcon triggerType={triggerType} />
                          <RunStatusIcon status={option.status} />
                          <span className="min-w-0 flex-1 truncate font-mono" title={option.run_id}>
                            {option.run_id}
                          </span>
                          <span className="shrink-0 text-muted-foreground">
                            {formatTime(option.started_at)}
                          </span>
                        </div>
                      </button>
                      {onDeleteRun ? (
                        <button
                          type="button"
                          onClick={() => {
                            if (canDelete) onDeleteRun(option.run_id);
                          }}
                          disabled={!canDelete}
                          title={canDelete ? "Delete run" : "Stop run before deleting"}
                          aria-label={`Delete run ${option.run_id}`}
                          className="m-1 flex h-7 w-7 items-center justify-center self-center rounded text-destructive hover:bg-destructive/10 disabled:pointer-events-none disabled:opacity-35"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </button>
                      ) : (
                        <span />
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </div>

        <ColumnResizeHandle
          label="Resize Run and Run Event columns"
          value={columnBoundaryPercent(columnRatios, 0)}
          onPointerDown={(event) => startResizeColumns(0, event)}
          onKeyDown={(event) => resizeColumnsWithKeyboard(0, event)}
        />

        <div aria-label="Run event list" className="flex min-h-0 min-w-0 flex-col">
          <div className="flex h-9 shrink-0 items-center border-b border-border px-3">
            <span className="text-xs font-semibold">Run Event</span>
            <span className="ml-auto text-xs text-muted-foreground">
              {filteredEventItems.length}/{sortedEvents.length}
            </span>
          </div>
          <EventFilterControls
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
          <div className="min-h-0 flex-1 overflow-auto">
            {filteredEventItems.length === 0 ? (
              <div className="p-3 text-sm text-muted-foreground">
                {sortedEvents.length > 0 ? "No matching events" : "No run events"}
              </div>
            ) : (
              <ul className="divide-y divide-border">
                {filteredEventItems.map(({ event, key }) => (
                  <li key={key}>
                    <button
                      type="button"
                      onClick={() => setSelectedKey(key)}
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
                      <span className="truncate text-right text-muted-foreground">
                        {formatTimeMs(event.timestamp)}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>

        <ColumnResizeHandle
          label="Resize Run Event and Event Detail columns"
          value={columnBoundaryPercent(columnRatios, 1)}
          onPointerDown={(event) => startResizeColumns(1, event)}
          onKeyDown={(event) => resizeColumnsWithKeyboard(1, event)}
        />

        <div aria-label="Event detail" className="flex min-h-0 min-w-0 flex-col">
          <div className="flex h-9 shrink-0 items-center border-b border-border px-3">
            <span className="text-xs font-semibold">Event Detail</span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto p-3">
            {selectedEvent ? (
              <EventDetail event={selectedEvent} />
            ) : (
              <div className="text-sm text-muted-foreground">Select an event</div>
            )}
          </div>
        </div>
      </div>
    </section>
  );
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

function EventFilterControls({
  open,
  mode,
  types,
  selectedNodes,
  keyword,
  eventTypes,
  nodes,
  activeCount,
  filteredCount,
  totalCount,
  onOpenChange,
  onModeChange,
  onTypesChange,
  onNodesChange,
  onKeywordChange,
  onClear,
}: {
  open: boolean;
  mode: EventFilterMode;
  types: string[];
  selectedNodes: string[];
  keyword: string;
  eventTypes: string[];
  nodes: string[];
  activeCount: number;
  filteredCount: number;
  totalCount: number;
  onOpenChange: (value: boolean) => void;
  onModeChange: (value: EventFilterMode) => void;
  onTypesChange: (value: string[]) => void;
  onNodesChange: (value: string[]) => void;
  onKeywordChange: (value: string) => void;
  onClear: () => void;
}) {
  const menuRef = useRef<HTMLDivElement | null>(null);
  const popoverRef = useRef<HTMLDivElement | null>(null);
  const [popoverPosition, setPopoverPosition] = useState<EventFilterPopoverPosition | null>(null);
  const hasActiveFilters = activeCount > 0;

  useEffect(() => {
    if (!open) {
      setPopoverPosition(null);
      return;
    }

    const updatePosition = () => {
      const anchor = menuRef.current;
      if (!anchor) return;
      setPopoverPosition(eventFilterPopoverPosition(anchor));
    };

    updatePosition();
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    return () => {
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const closeOnPointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (menuRef.current?.contains(target) || popoverRef.current?.contains(target)) return;
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

  const popover =
    open && popoverPosition
      ? createPortal(
          <EventFilterPopover
            ref={popoverRef}
            mode={mode}
            types={types}
            selectedNodes={selectedNodes}
            keyword={keyword}
            eventTypes={eventTypes}
            nodes={nodes}
            filteredCount={filteredCount}
            totalCount={totalCount}
            position={popoverPosition}
            onModeChange={onModeChange}
            onTypesChange={onTypesChange}
            onNodesChange={onNodesChange}
            onKeywordChange={onKeywordChange}
            onClose={() => onOpenChange(false)}
          />,
          document.body
        )
      : null;

  return (
    <>
      <div ref={menuRef} className="sticky top-0 z-20 border-b border-border bg-panel px-1.5 py-0.5">
        <div className="flex h-6 items-center gap-1">
          <Button
            variant={hasActiveFilters ? "outline" : "ghost"}
            size="icon"
            onClick={() => onOpenChange(!open)}
            title="Filter events"
            aria-label="Filter events"
            aria-expanded={open}
            className="relative h-6 w-6 shrink-0"
          >
            <Filter className="h-3.5 w-3.5" />
            {hasActiveFilters ? (
              <span className="absolute -right-1 -top-1 min-w-3 rounded-full bg-primary px-0.5 text-center font-mono text-[9px] leading-3 text-primary-foreground">
                {activeCount}
              </span>
            ) : null}
          </Button>
          <span className="min-w-0 truncate text-[10px] text-muted-foreground">
            {hasActiveFilters ? `${mode === "exclude" ? "Excl" : "Match"} ` : ""}
            {filteredCount}/{totalCount}
          </span>
          {hasActiveFilters ? (
            <Button
              variant="ghost"
              size="icon"
              onClick={onClear}
              title="Clear event filters"
              aria-label="Clear event filters"
              className="ml-auto h-6 w-6"
            >
              <X className="h-3 w-3" />
            </Button>
          ) : null}
        </div>
      </div>
      {popover}
    </>
  );
}

const EventFilterPopover = forwardRef<HTMLDivElement, {
  mode: EventFilterMode;
  types: string[];
  selectedNodes: string[];
  keyword: string;
  eventTypes: string[];
  nodes: string[];
  filteredCount: number;
  totalCount: number;
  position: EventFilterPopoverPosition;
  onModeChange: (value: EventFilterMode) => void;
  onTypesChange: (value: string[]) => void;
  onNodesChange: (value: string[]) => void;
  onKeywordChange: (value: string) => void;
  onClose: () => void;
}>(function EventFilterPopover(
  {
    mode,
    types,
    selectedNodes,
    keyword,
    eventTypes,
    nodes,
    filteredCount,
    totalCount,
    position,
    onModeChange,
    onTypesChange,
    onNodesChange,
    onKeywordChange,
    onClose,
  },
  ref
) {
  const style: CSSProperties = {
    left: position.left,
    width: position.width,
    maxHeight: position.maxHeight,
    top: position.top,
    bottom: position.bottom,
  };

  return (
    <div
      ref={ref}
      className={cn(
        "fixed z-[100] overflow-hidden rounded-md border border-border bg-panel shadow-lg",
        position.anchor === "above" ? "origin-bottom" : "origin-top"
      )}
      style={style}
    >
      <div className="flex items-center justify-between gap-2 border-b border-border p-2">
        <div className="inline-flex rounded-md border border-border bg-background p-0.5">
          {(["include", "exclude"] as const).map((option) => (
            <button
              key={option}
              type="button"
              aria-pressed={mode === option}
              onClick={() => onModeChange(option)}
              className={cn(
                "h-7 rounded px-2 text-xs transition-colors",
                mode === option
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-accent"
              )}
            >
              {option === "include" ? "Include" : "Exclude"}
            </button>
          ))}
        </div>
        <span className="shrink-0 text-[11px] text-muted-foreground">
          {filteredCount}/{totalCount} events
        </span>
        <Button
          variant="ghost"
          size="icon"
          onClick={onClose}
          title="Close filters"
          aria-label="Close filters"
          className="h-7 w-7"
        >
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>
      <div className="grid gap-2 overflow-auto p-2" style={{ maxHeight: position.bodyMaxHeight }}>
        <label className="relative block min-w-0">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={keyword}
            onChange={(event) => onKeywordChange(event.target.value)}
            placeholder="Keyword"
            className="h-8 pl-7 text-xs"
          />
        </label>
        <EventFilterFacet
          title="Event type"
          options={eventTypes}
          selectedValues={types}
          emptyLabel="No event types"
          onChange={onTypesChange}
        />
        <EventFilterFacet
          title="Node"
          options={nodes}
          selectedValues={selectedNodes}
          emptyLabel="No nodes"
          onChange={onNodesChange}
        />
      </div>
    </div>
  );
});

function eventFilterPopoverPosition(anchor: HTMLElement): EventFilterPopoverPosition {
  const rect = anchor.getBoundingClientRect();
  const viewportWidth = window.innerWidth;
  const viewportHeight = window.innerHeight;
  const viewportMaxWidth = Math.max(0, viewportWidth - FILTER_POPOVER_MARGIN * 2);
  const viewportMaxHeight = Math.max(0, viewportHeight - FILTER_POPOVER_MARGIN * 2);
  const width = Math.min(
    Math.max(rect.width, FILTER_POPOVER_MIN_WIDTH),
    FILTER_POPOVER_MAX_WIDTH,
    viewportMaxWidth
  );
  const left = clampNumber(
    rect.left,
    FILTER_POPOVER_MARGIN,
    Math.max(FILTER_POPOVER_MARGIN, viewportWidth - width - FILTER_POPOVER_MARGIN)
  );
  const belowSpace = viewportHeight - rect.bottom - FILTER_POPOVER_GAP - FILTER_POPOVER_MARGIN;
  const aboveSpace = rect.top - FILTER_POPOVER_GAP - FILTER_POPOVER_MARGIN;
  const anchorPosition =
    belowSpace < FILTER_POPOVER_MIN_AVAILABLE_HEIGHT && aboveSpace > belowSpace ? "above" : "below";
  const availableHeight = Math.max(0, anchorPosition === "above" ? aboveSpace : belowSpace);
  const maxHeight = Math.min(FILTER_POPOVER_MAX_HEIGHT, viewportMaxHeight, availableHeight);
  const bodyMaxHeight = Math.max(0, maxHeight - FILTER_POPOVER_HEADER_HEIGHT);

  if (anchorPosition === "above") {
    return {
      anchor: "above",
      left,
      width,
      maxHeight,
      bodyMaxHeight,
      bottom: viewportHeight - rect.top + FILTER_POPOVER_GAP,
    };
  }

  return {
    anchor: "below",
    left,
    width,
    maxHeight,
    bodyMaxHeight,
    top: rect.bottom + FILTER_POPOVER_GAP,
  };
}

function clampNumber(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

function EventFilterFacet({
  title,
  options,
  selectedValues,
  emptyLabel,
  onChange,
}: {
  title: string;
  options: string[];
  selectedValues: string[];
  emptyLabel: string;
  onChange: (values: string[]) => void;
}) {
  const selectedSet = new Set(selectedValues);

  return (
    <section className="overflow-hidden rounded-md border border-border bg-background">
      <div className="flex h-8 items-center gap-2 border-b border-border px-2">
        <span className="truncate text-xs font-medium">{title}</span>
        <span className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground">
          {selectedValues.length > 0 ? selectedValues.length : "all"}
        </span>
        {selectedValues.length > 0 ? (
          <button
            type="button"
            onClick={() => onChange([])}
            className="shrink-0 text-[11px] text-muted-foreground hover:text-foreground"
          >
            Clear
          </button>
        ) : null}
      </div>
      {options.length === 0 ? (
        <div className="px-2 py-2 text-xs text-muted-foreground">{emptyLabel}</div>
      ) : (
        <div className="max-h-32 overflow-auto p-1">
          {options.map((option) => {
            const checked = selectedSet.has(option);
            return (
              <label
                key={option}
                className={cn(
                  "flex min-w-0 cursor-pointer items-center gap-2 rounded px-2 py-1 text-xs hover:bg-accent/60",
                  checked && "bg-accent text-accent-foreground"
                )}
              >
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={() => onChange(toggleFilterValue(selectedValues, option))}
                  className="h-3.5 w-3.5 accent-primary"
                />
                <span className="truncate font-mono">{option}</span>
              </label>
            );
          })}
        </div>
      )}
    </section>
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

function eventListKey(event: RuntimeEvent, index: number): string {
  return `${event.id || event.run_id || "event"}-${index}`;
}

function readStoredPanelHeight(): number {
  if (typeof window === "undefined") return DEFAULT_PANEL_HEIGHT;
  try {
    const raw = window.localStorage.getItem(PANEL_HEIGHT_STORAGE_KEY);
    const parsed = raw ? Number(raw) : NaN;
    if (!Number.isFinite(parsed)) return DEFAULT_PANEL_HEIGHT;
    const maxHeight = Math.max(MIN_PANEL_HEIGHT, window.innerHeight - 160);
    return Math.max(MIN_PANEL_HEIGHT, Math.min(maxHeight, parsed));
  } catch {
    return DEFAULT_PANEL_HEIGHT;
  }
}

function writeStoredPanelHeight(height: number): void {
  if (typeof window === "undefined" || !Number.isFinite(height)) return;
  try {
    window.localStorage.setItem(PANEL_HEIGHT_STORAGE_KEY, String(Math.round(height)));
  } catch {
    // Ignore storage errors; height persistence is best effort.
  }
}

function readStoredEventFilters(): StoredEventFilters {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(EVENT_FILTER_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as StoredEventFilters;
    return {
      open: typeof parsed.open === "boolean" ? parsed.open : false,
      mode: parsed.mode === "exclude" ? "exclude" : "include",
      types: Array.isArray(parsed.types) ? parsed.types.filter(isStringValue) : [],
      nodes: Array.isArray(parsed.nodes) ? parsed.nodes.filter(isStringValue) : [],
      keyword: typeof parsed.keyword === "string" ? parsed.keyword : "",
    };
  } catch {
    return {};
  }
}

function writeStoredEventFilters(filters: StoredEventFilters): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(EVENT_FILTER_STORAGE_KEY, JSON.stringify(filters));
  } catch {
    // Ignore storage errors; filters still work for the current session.
  }
}

function isStringValue(value: unknown): value is string {
  return typeof value === "string";
}

function isRunActive(status: string): boolean {
  return status === "pending" || status === "running";
}

function RunSourceIcon({ triggerType }: { triggerType?: TriggerType }) {
  let SourceIcon = Play;
  let label = "Run";
  if (triggerType === "chat") {
    SourceIcon = MessageCircle;
    label = "Chat";
  } else if (triggerType === "webhook") {
    SourceIcon = Webhook;
    label = "Webhook";
  } else if (triggerType === "schedule") {
    SourceIcon = Clock3;
    label = "Schedule";
  }
  return (
    <span
      data-run-source={triggerType ?? "direct"}
      aria-label={label}
      title={label}
      className={cn(
        "flex h-5 w-5 shrink-0 items-center justify-center rounded-md border border-border bg-muted/60 shadow-sm",
        triggerType ? "text-primary" : "text-muted-foreground"
      )}
    >
      <SourceIcon className="h-3.5 w-3.5" />
    </span>
  );
}

function RunStatusIcon({ status }: { status: string }) {
  let StatusIcon = Circle;
  let iconClassName = "text-muted-foreground";
  switch (status) {
    case "pending":
      StatusIcon = Clock3;
      break;
    case "running":
      StatusIcon = Loader2;
      iconClassName = "animate-spin text-cyan-700 dark:text-cyan-300";
      break;
    case "paused":
    case "interrupted":
      StatusIcon = Pause;
      iconClassName = "text-amber-700 dark:text-amber-300";
      break;
    case "completed":
    case "finished":
      StatusIcon = Check;
      iconClassName = "text-emerald-700 dark:text-emerald-300";
      break;
    case "failed":
      StatusIcon = X;
      iconClassName = "text-destructive";
      break;
    case "canceled":
      StatusIcon = Ban;
      iconClassName = "text-destructive";
      break;
  }
  return (
    <span
      data-run-status={status}
      aria-label={status}
      title={status}
      className="flex h-4 w-4 shrink-0 items-center justify-center"
    >
      <StatusIcon className={cn("h-3.5 w-3.5", iconClassName)} />
    </span>
  );
}

function eventMatchesFilters(
  event: RuntimeEvent,
  filters: { mode: EventFilterMode; types: string[]; nodes: string[]; keyword: string }
): boolean {
  if (!hasEventFilterCriteria(filters)) return true;
  const matches = eventMatchesPositiveFilters(event, filters);
  return filters.mode === "exclude" ? !matches : matches;
}

function eventMatchesPositiveFilters(
  event: RuntimeEvent,
  filters: { types: string[]; nodes: string[]; keyword: string }
): boolean {
  if (filters.types.length > 0 && !filters.types.includes(event.type)) return false;
  const nodeId = event.node_id ?? "";
  if (filters.nodes.length > 0 && !filters.nodes.includes(nodeId)) return false;
  const keyword = filters.keyword.trim().toLowerCase();
  if (!keyword) return true;
  return eventSearchText(event).includes(keyword);
}

function hasEventFilterCriteria(filters: { types: string[]; nodes: string[]; keyword: string }): boolean {
  return filters.types.length > 0 || filters.nodes.length > 0 || filters.keyword.trim() !== "";
}

function toggleFilterValue(values: string[], value: string): string[] {
  return values.includes(value) ? values.filter((item) => item !== value) : [...values, value];
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

function timeRank(value?: string): number {
  if (!value) return 0;
  const ts = Date.parse(value);
  return Number.isNaN(ts) ? 0 : ts;
}

export function resizeRunPanelColumnRatios(
  current: ColumnRatios,
  boundary: 0 | 1,
  deltaPixels: number,
  availableWidth: number
): ColumnRatios {
  if (!Number.isFinite(deltaPixels) || !Number.isFinite(availableWidth) || availableWidth <= 0) return current;
  const totalRatio = current[0] + current[1] + current[2];
  if (totalRatio <= 0) return current;

  const widths: ColumnRatios = [
    (availableWidth * current[0]) / totalRatio,
    (availableWidth * current[1]) / totalRatio,
    (availableWidth * current[2]) / totalRatio,
  ];
  const leftIndex = boundary;
  const rightIndex = boundary + 1;
  const pairWidth = widths[leftIndex] + widths[rightIndex];
  const pairMinimum = MIN_COLUMN_WIDTHS[leftIndex] + MIN_COLUMN_WIDTHS[rightIndex];
  const minimumScale = Math.min(1, pairWidth / pairMinimum);
  const leftMinimum = MIN_COLUMN_WIDTHS[leftIndex] * minimumScale;
  const rightMinimum = MIN_COLUMN_WIDTHS[rightIndex] * minimumScale;
  const clampedDelta = Math.max(
    leftMinimum - widths[leftIndex],
    Math.min(widths[rightIndex] - rightMinimum, deltaPixels)
  );

  widths[leftIndex] += clampedDelta;
  widths[rightIndex] -= clampedDelta;
  return [
    (widths[0] / availableWidth) * totalRatio,
    (widths[1] / availableWidth) * totalRatio,
    (widths[2] / availableWidth) * totalRatio,
  ];
}

function columnGridTemplate(ratios: ColumnRatios): string {
  return `minmax(0, ${ratios[0]}fr) ${COLUMN_SEPARATOR_WIDTH}px minmax(0, ${ratios[1]}fr) ${COLUMN_SEPARATOR_WIDTH}px minmax(0, ${ratios[2]}fr)`;
}

function columnBoundaryPercent(ratios: ColumnRatios, boundary: 0 | 1): number {
  const total = ratios[0] + ratios[1] + ratios[2];
  const occupied = boundary === 0 ? ratios[0] : ratios[0] + ratios[1];
  return total > 0 ? Math.round((occupied / total) * 100) : 0;
}

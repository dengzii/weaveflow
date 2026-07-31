import { forwardRef, useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import { createPortal } from "react-dom";
import { Filter, Search, X } from "lucide-react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { cn } from "../../lib/utils";
import { toggleFilterValue } from "./runStatusModel";
import type { EventFilterMode } from "./runStatusModel";

interface EventFilterPopoverPosition {
  anchor: "above" | "below";
  left: number;
  width: number;
  maxHeight: number;
  bodyMaxHeight: number;
  top?: number;
  bottom?: number;
}

const FILTER_POPOVER_GAP = 4;
const FILTER_POPOVER_MARGIN = 8;
const FILTER_POPOVER_MIN_WIDTH = 320;
const FILTER_POPOVER_MAX_WIDTH = 520;
const FILTER_POPOVER_MAX_HEIGHT = 384;
const FILTER_POPOVER_HEADER_HEIGHT = 45;
const FILTER_POPOVER_MIN_AVAILABLE_HEIGHT = 220;
export function RunEventFilterControls({
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
      <div ref={menuRef} className="ml-auto flex min-w-0 items-center gap-1">
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

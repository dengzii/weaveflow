import type { PointerEventHandler, RefObject } from "react";
import { Link } from "react-router-dom";
import {
  ArrowLeft,
  ArrowRightLeft,
  ChevronLeft,
  ChevronRight,
  Database,
  GitGraph,
  Pause,
  Play,
  Radio,
  RefreshCw,
  Rows3,
  SkipBack,
  SkipForward,
} from "lucide-react";
import type { LiveState, RunDetail, RunSummary } from "../../replay/types";
import { formatDuration, formatTime, prettyJSON } from "../../replay/utils";
import { parseSourceGraph } from "../graph";
import { RunMetadataSection } from "../../replay/components/RunMetadataSection";
import { visibleLiveStatus } from "../useLiveMode";
import type { LiveSocketState } from "../useLiveMode";
import { Badge } from "../../../components/ui/badge";
import { Button } from "../../../components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../../../components/ui/select";
import { cn } from "../../../lib/utils";
import { ArtifactPreviewSection } from "./ArtifactPreviewSection";

function runOptionValue(item: RunSummary): string {
  return `${item.source_id}::${item.run.run_id}`;
}

function runOptionLabel(item: RunSummary): string {
  return `${item.source_name || item.source_id} | ${item.run.run_id}`;
}

export function ReplaySidebar({
  cacheDir,
  detail,
  runs,
  selectedRunId,
  selectedSourceId,
  replayIndex,
  isPlaying,
  statusMessage,
  isLiveMode,
  liveState,
  liveSocketState,
  liveBadge,
  liveDuration,
  viewMode,
  sidebarCollapsed,
  sidebarWidth,
  liveEventsListRef,
  onExpandSidebar,
  onCollapseSidebar,
  onToggleLiveMode,
  onToggleViewMode,
  onRelayout,
  onRefreshRuns,
  onSelectRun,
  onReplayIndexChange,
  onToggleReplay,
  onSelectLiveEvent,
  onResizePointerDown,
  onResizePointerMove,
  onResizePointerUp,
}: {
  cacheDir: string;
  detail: RunDetail | null;
  runs: RunSummary[];
  selectedRunId: string;
  selectedSourceId: string;
  replayIndex: number;
  isPlaying: boolean;
  statusMessage: string;
  isLiveMode: boolean;
  liveState: LiveState | null;
  liveSocketState: LiveSocketState;
  liveBadge: string;
  liveDuration: number;
  viewMode: "flow" | "mermaid";
  sidebarCollapsed: boolean;
  sidebarWidth: number;
  liveEventsListRef: RefObject<HTMLDivElement | null>;
  onExpandSidebar: () => void;
  onCollapseSidebar: () => void;
  onToggleLiveMode: () => void;
  onToggleViewMode: () => void;
  onRelayout: () => void;
  onRefreshRuns: () => void;
  onSelectRun: (item: RunSummary) => void;
  onReplayIndexChange: (index: number) => void;
  onToggleReplay: () => void;
  onSelectLiveEvent: (index: number) => void;
  onResizePointerDown: PointerEventHandler<HTMLDivElement>;
  onResizePointerMove: PointerEventHandler<HTMLDivElement>;
  onResizePointerUp: PointerEventHandler<HTMLDivElement>;
}) {
  const current = detail?.replay?.[replayIndex] ?? null;
  const selectedRunValue =
    selectedRunId && selectedSourceId ? `${selectedSourceId}::${selectedRunId}` : undefined;
  const sourceGraph = detail ? parseSourceGraph(detail.source.graph) : null;
  const visibleNodeCount =
    sourceGraph?.nodes.filter((node) => node.type !== "start" && node.type !== "end").length ?? 0;
  const edgeCount = sourceGraph?.edges.length ?? 0;
  const liveGraphRef = liveState?.graph_ref || detail?.summary.graph_ref || detail?.run.graph_id || "-";
  const liveSourceName = liveState?.source_name || detail?.summary.source_name || "Neo Agent";
  const liveGraphJSON = detail?.source.graph ?? liveState?.graph;
  const liveStatusText = isLiveMode ? visibleLiveStatus(statusMessage, liveSocketState) : "";
  const artifactPayload = (() => {
    if (current?.event.type !== "artifact.created" || !detail) return null;
    const payload = (current.event.payload ?? {}) as Record<string, unknown>;
    const id = String(payload.artifact_id ?? "");
    if (!id) return null;
    return {
      id,
      mimeType: String(payload.mime_type ?? ""),
      artifactType: String(payload.type ?? ""),
      runId: detail.run.run_id,
      sourceId: detail.source.id,
    };
  })();

  if (sidebarCollapsed) {
    return (
      <div className="pointer-events-auto flex flex-col gap-2">
        <div className="flex flex-col gap-1 border border-l-0 border-border bg-background/92 p-2 shadow-2xl backdrop-blur-xl">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="h-8 w-8 rounded-lg text-foreground hover:bg-muted"
            onClick={onExpandSidebar}
            title="Expand sidebar"
          >
            <ChevronRight className="h-4 w-4" />
          </Button>
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-foreground text-background">
            <Database className="h-4 w-4" />
          </div>
        </div>
      </div>
    );
  }

  return (
    <div
      className="pointer-events-auto relative flex h-full self-stretch flex-col overflow-hidden border border-l-0 border-border bg-background/92 text-foreground shadow-2xl backdrop-blur-xl"
      style={{ width: sidebarWidth }}
    >
      <div
        className="absolute right-0 top-0 z-30 h-full w-1 cursor-ew-resize transition-colors hover:bg-muted/50 active:bg-muted"
        onPointerDown={onResizePointerDown}
        onPointerMove={onResizePointerMove}
        onPointerUp={onResizePointerUp}
        onPointerCancel={onResizePointerUp}
      />

      <div className="flex items-center gap-1.5 border-b border-border px-3 py-3">
        <Link to="/">
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7 shrink-0 rounded-lg text-foreground hover:bg-muted"
          >
            <ArrowLeft className="h-3.5 w-3.5" />
          </Button>
        </Link>
        <div className="flex min-w-0 flex-1 items-center gap-1.5">
          <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-foreground text-background">
            <Database className="h-3.5 w-3.5" />
          </div>
          <span className="truncate text-sm font-semibold text-foreground">Replay</span>
          {isLiveMode ? (
            <Badge
              variant="outline"
              className="rounded-full border-border bg-card text-[10px] uppercase tracking-[0.14em] text-rose-300"
            >
              Live
            </Badge>
          ) : null}
        </div>
        <Link to="/debug/replay/old">
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7 shrink-0 rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground"
            title="Old view"
          >
            <ArrowRightLeft className="h-3.5 w-3.5" />
          </Button>
        </Link>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className={cn(
            "h-7 w-7 shrink-0 rounded-lg hover:bg-muted",
            isLiveMode ? "text-rose-400" : "text-muted-foreground hover:text-foreground"
          )}
          onClick={onToggleLiveMode}
          title={isLiveMode ? "Leave live mode" : "Enter live mode"}
        >
          <Radio className="h-3.5 w-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className={cn(
            "h-7 w-7 shrink-0 rounded-lg hover:bg-muted",
            viewMode === "mermaid"
              ? "text-violet-400"
              : "text-muted-foreground hover:text-foreground"
          )}
          onClick={onToggleViewMode}
          title={viewMode === "mermaid" ? "Switch to Flow" : "Switch to Mermaid"}
        >
          <GitGraph className="h-3.5 w-3.5" />
        </Button>
        {viewMode === "flow" ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="h-7 w-7 shrink-0 rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground"
            onClick={onRelayout}
            title="Re-layout"
          >
            <Rows3 className="h-3.5 w-3.5" />
          </Button>
        ) : null}
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="h-7 w-7 shrink-0 rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground"
          onClick={onCollapseSidebar}
          title="Collapse"
        >
          <ChevronLeft className="h-3.5 w-3.5" />
        </Button>
      </div>

      <div className="shrink-0 border-b border-border bg-background/95 px-3 py-3 backdrop-blur-xl">
        {isLiveMode ? (
          <div className="flex items-center justify-between gap-3">
            <div className="min-w-0">
              <div className="truncate text-xs font-semibold text-rose-200">{liveSourceName}</div>
              <div className="truncate text-[11px] text-rose-200/65">{liveGraphRef}</div>
            </div>
            <Badge
              variant="outline"
              className={cn(
                "rounded-full border-rose-400/30 bg-rose-950/40 text-[10px] uppercase tracking-[0.14em]",
                liveSocketState === "disconnected"
                  ? "text-muted-foreground"
                  : liveState?.running
                    ? "text-rose-300"
                    : "text-amber-200"
              )}
            >
              {liveBadge}
            </Badge>
          </div>
        ) : (
          <div className="flex items-center gap-1.5">
            <Select
              value={selectedRunValue}
              onValueChange={(value) => {
                const target = runs.find((item) => runOptionValue(item) === value);
                if (target) onSelectRun(target);
              }}
            >
              <SelectTrigger className="h-8 rounded-lg border-border bg-card/80 text-xs text-foreground">
                <SelectValue placeholder="Select a replay" />
              </SelectTrigger>
              <SelectContent>
                {runs.map((item) => (
                  <SelectItem key={runOptionValue(item)} value={runOptionValue(item)}>
                    {runOptionLabel(item)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="h-8 w-8 shrink-0 rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground"
              onClick={onRefreshRuns}
            >
              <RefreshCw className="h-3.5 w-3.5" />
            </Button>
          </div>
        )}
        {!isLiveMode || liveStatusText ? (
          <div className="mt-1 flex items-center gap-2 px-1">
            <span className="flex-1 truncate text-[11px] text-muted-foreground">
              {isLiveMode ? liveStatusText : statusMessage}
            </span>
          </div>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        {detail ? (
          <div className="mt-3 divide-y divide-border">
            {isLiveMode ? (
              <div className="grid grid-cols-2 gap-x-4 gap-y-2 pb-3">
                <Metric
                  label="Nodes / Edges"
                  value={`${visibleNodeCount} / ${edgeCount}`}
                  className={liveState?.running ? undefined : "col-span-2"}
                />
                {liveState?.running ? (
                  <Metric label="Duration" value={formatDuration(liveDuration)} live />
                ) : null}
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-x-4 gap-y-2 pb-3">
                <Metric
                  label="Graph"
                  value={detail.summary.graph_ref || detail.run.graph_id || "-"}
                />
                <Metric label="Duration" value={formatDuration(detail.summary.duration_ms)} />
              </div>
            )}

            {!isLiveMode ? (
              <div className="py-3">
                <div className="flex items-center gap-2">
                  <span className="text-xs tabular-nums text-muted-foreground">
                    {detail.replay.length ? `${replayIndex + 1}/${detail.replay.length}` : "0/0"}
                  </span>
                  <div className="flex flex-1 items-center justify-end gap-1">
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      className="h-7 w-7 rounded-full"
                      onClick={() => onReplayIndexChange(replayIndex - 1)}
                      disabled={replayIndex <= 0}
                    >
                      <SkipBack className="h-3.5 w-3.5" />
                    </Button>
                    <Button
                      type="button"
                      size="icon"
                      variant="outline"
                      className="h-7 w-7 rounded-full"
                      onClick={onToggleReplay}
                      disabled={detail.replay.length === 0}
                    >
                      {isPlaying ? (
                        <Pause className="h-3.5 w-3.5" />
                      ) : (
                        <Play className="h-3.5 w-3.5" />
                      )}
                    </Button>
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      className="h-7 w-7 rounded-full"
                      onClick={() => onReplayIndexChange(replayIndex + 1)}
                      disabled={replayIndex >= detail.replay.length - 1}
                    >
                      <SkipForward className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                </div>
                <input
                  type="range"
                  className="mt-2 w-full accent-amber-400"
                  min={0}
                  max={Math.max(0, detail.replay.length - 1)}
                  value={replayIndex}
                  onChange={(event) => onReplayIndexChange(Number(event.target.value))}
                />
              </div>
            ) : null}

            <div className="py-3">
              <div className="flex items-center justify-between gap-2">
                <div className="min-w-0">
                  <div className="truncate text-sm font-semibold text-foreground">
                    {current?.title || (isLiveMode ? "Waiting for next event" : "No event")}
                  </div>
                  {(current?.subtitle || current?.event.node_id) ? (
                    <div className="truncate text-[11px] text-muted-foreground">
                      {current?.subtitle || current?.event.node_id}
                    </div>
                  ) : null}
                </div>
                <div className="flex shrink-0 flex-col items-end gap-1">
                  <Badge
                    variant="outline"
                    className="rounded-full border-border bg-muted text-[10px] text-foreground"
                  >
                    {current?.event.type || detail.run.status}
                  </Badge>
                  {current?.timestamp ? (
                    <span className="text-[10px] text-muted-foreground">
                      {formatTime(current.timestamp)}
                    </span>
                  ) : null}
                </div>
              </div>
            </div>

            {artifactPayload ? (
              <div className="py-3">
                <div className="mb-1.5 flex items-center gap-2">
                  <span className="text-xs font-medium text-muted-foreground">Artifact</span>
                  <span className="rounded-full bg-violet-500/15 px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-[0.12em] text-violet-300 ring-1 ring-violet-400/25">
                    {artifactPayload.artifactType || artifactPayload.mimeType || "file"}
                  </span>
                </div>
                <div className="mb-2 truncate font-mono text-[10px] text-muted-foreground">
                  {artifactPayload.id}
                </div>
                <ArtifactPreviewSection
                  cacheDir={cacheDir}
                  runId={artifactPayload.runId}
                  sourceId={artifactPayload.sourceId}
                  artifactId={artifactPayload.id}
                  mimeType={artifactPayload.mimeType}
                />
              </div>
            ) : null}

            {isLiveMode ? (
              <>
                <div className="py-3">
                  <div className="mb-2 flex items-center justify-between gap-2">
                    <span className="text-xs font-medium text-muted-foreground">Events</span>
                    <span className="rounded-full bg-muted px-1.5 py-0.5 text-[9px] tabular-nums text-muted-foreground">
                      {detail.replay.length}
                    </span>
                  </div>
                  <div ref={liveEventsListRef} className="max-h-[220px] space-y-1 overflow-auto">
                    {detail.replay.length ? (
                      detail.replay.map((item, index) => (
                        <button
                          key={`${item.index}:${item.event.id || item.timestamp}:${item.event.type}`}
                          data-live-event-index={index}
                          type="button"
                          onClick={() => onSelectLiveEvent(index)}
                          className={cn(
                            "block w-full rounded-lg px-2.5 py-2 text-left transition",
                            index === replayIndex
                              ? "bg-amber-500/16 text-foreground"
                              : "text-muted-foreground hover:bg-muted/50"
                          )}
                        >
                          <div className="truncate text-xs font-medium">
                            {item.title || item.event.type || "Event"}
                          </div>
                          <div className="mt-0.5 flex items-center justify-between gap-2 text-[10px] text-muted-foreground">
                            <span className="min-w-0 flex-1 truncate">
                              {item.subtitle || item.event.node_id || item.event.type}
                            </span>
                            <span className="shrink-0">{formatTime(item.timestamp)}</span>
                          </div>
                        </button>
                      ))
                    ) : (
                      <div className="px-2.5 py-3 text-xs text-muted-foreground">
                        Waiting for live events.
                      </div>
                    )}
                  </div>
                </div>

                <div className="py-3">
                  <div className="mb-2 text-xs font-medium text-muted-foreground">
                    Current Event Payload
                  </div>
                  <pre className="max-h-[180px] overflow-auto rounded-lg bg-card p-3 font-mono text-[11px] leading-5 text-foreground">
                    {prettyJSON(current?.event.payload)}
                  </pre>
                </div>

                <div className="py-3">
                  <div className="mb-2 text-xs font-medium text-muted-foreground">Graph JSON</div>
                  <pre className="max-h-[240px] overflow-auto rounded-lg bg-card p-3 font-mono text-[11px] leading-5 text-foreground">
                    {prettyJSON(liveGraphJSON)}
                  </pre>
                </div>
              </>
            ) : null}

            {!isLiveMode && detail.metadata ? (
              <div className="py-3">
                <RunMetadataSection detail={detail} compact />
              </div>
            ) : null}
          </div>
        ) : (
          <div className="mt-3 rounded-xl border border-dashed border-border bg-card/60 px-4 py-6 text-center text-sm text-muted-foreground">
            {isLiveMode
              ? "Waiting for live graph snapshot. Start a new request to stream the graph here."
              : "Select a replay to load graph history."}
          </div>
        )}
      </div>
    </div>
  );
}

function Metric({
  label,
  value,
  live,
  className,
}: {
  label: string;
  value: string;
  live?: boolean;
  className?: string;
}) {
  return (
    <div className={cn("min-w-0", className)}>
      <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
        {label}
        {live ? <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-rose-400" /> : null}
      </div>
      <div className="truncate pt-1 text-sm font-semibold text-foreground">{value}</div>
    </div>
  );
}

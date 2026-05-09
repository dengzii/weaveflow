import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, apiDelete, buildUrl } from "./api";
import type {
  RunDetail,
  RunSummary,
  RunsResponse,
} from "./types";
import { ReplayGraphCanvas } from "./ReplayGraphCanvas";
import { MermaidGraphCanvas } from "./MermaidGraphCanvas";
import { ReplayPayloadPanel } from "./components/ReplayPayloadPanel";
import { ReplaySidebar } from "./components/ReplaySidebar";
import { useLiveMode } from "./useLiveMode";
import type { PageMode } from "./useLiveMode";

const DEFAULT_CACHE_DIR =
  (document.body.dataset.defaultCacheDir as string | undefined)?.trim() || "neo_data";

export function ReplayPage({ routeMode = "history" }: { routeMode?: PageMode }) {
  const navigate = useNavigate();
  const [status, setStatus] = useState({ message: "Preparing", summary: "" });
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [selectedRunId, setSelectedRunId] = useState("");
  const [selectedSourceId, setSelectedSourceId] = useState("");
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [replayIndex, setReplayIndex] = useState(0);
  const [isPlaying, setIsPlaying] = useState(false);
  const [layoutVersion, setLayoutVersion] = useState(0);
  const [viewMode, setViewMode] = useState<"flow" | "mermaid">("flow");
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(320);
  const sidebarWidthRef = useRef(320);
  const resizeDragRef = useRef<{ startX: number; startWidth: number } | null>(null);

  const timerRef = useRef<number | null>(null);
  const replayLengthRef = useRef(0);

  useEffect(() => {
    replayLengthRef.current = detail?.replay?.length ?? 0;
  }, [detail]);

  useEffect(() => {
    return () => {
      if (timerRef.current) clearInterval(timerRef.current);
    };
  }, []);

  function stopReplay() {
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
    setIsPlaying(false);
  }

  const {
    mode,
    modeRef,
    isLiveMode,
    liveState,
    liveSocketState,
    liveDuration,
    liveEventsListRef,
    liveBadge,
  } = useLiveMode({
    requestedMode: routeMode,
    cacheDir: DEFAULT_CACHE_DIR,
    setDetail,
    setReplayIndex,
    setStatus,
    onEnterLive: () => {
      stopReplay();
      setDetail(null);
      setReplayIndex(0);
    },
    onExitLive: async () => {
      stopReplay();
      const target =
        runs.find(
          (item) => item.run.run_id === selectedRunId && item.source_id === selectedSourceId
        ) ?? runs[0];
      if (target) {
        await selectRun(DEFAULT_CACHE_DIR, target);
        return;
      }
      setDetail(null);
      setStatus({ message: "No replay runs available.", summary: "" });
    },
  });

  useEffect(() => {
    if (mode !== "live") return;
    const container = liveEventsListRef.current;
    if (!container) return;
    const target = container.querySelector<HTMLElement>(`[data-live-event-index="${replayIndex}"]`);
    target?.scrollIntoView({ block: "nearest" });
  }, [mode, replayIndex, detail?.replay.length, liveEventsListRef]);

  function onResizePointerDown(e: React.PointerEvent<HTMLDivElement>) {
    if (e.button !== 0) return;
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    resizeDragRef.current = { startX: e.clientX, startWidth: sidebarWidthRef.current };
  }

  function onResizePointerMove(e: React.PointerEvent<HTMLDivElement>) {
    if (!resizeDragRef.current) return;
    const { startX, startWidth } = resizeDragRef.current;
    const next = Math.max(260, Math.min(680, startWidth + (e.clientX - startX)));
    sidebarWidthRef.current = next;
    setSidebarWidth(next);
  }

  function onResizePointerUp() {
    resizeDragRef.current = null;
  }

  function handleReplayIndex(index: number) {
    stopReplay();
    const length = replayLengthRef.current;
    if (!length) return;
    setReplayIndex(Math.max(0, Math.min(index, length - 1)));
  }

  function toggleReplay() {
    if (timerRef.current) {
      stopReplay();
      return;
    }
    if (!replayLengthRef.current) return;
    setIsPlaying(true);
    timerRef.current = window.setInterval(() => {
      setReplayIndex((current) => {
        if (!replayLengthRef.current || current >= replayLengthRef.current - 1) {
          if (timerRef.current) clearInterval(timerRef.current);
          timerRef.current = null;
          setIsPlaying(false);
          return current;
        }
        return current + 1;
      });
    }, 900);
  }

  async function selectRun(baseDir: string, item: RunSummary) {
    stopReplay();
    setSelectedRunId(item.run.run_id);
    setSelectedSourceId(item.source_id);
    setReplayIndex(0);
    setStatus({ message: "Loading replay detail...", summary: "" });

    try {
      const url = buildUrl(`/api/run/${encodeURIComponent(item.run.run_id)}`, baseDir, {
        source: item.source_id,
      });
      const runDetail = await api<RunDetail>(url);
      setDetail(runDetail);
      setStatus({
        message: "",
        summary: `${runDetail.summary.source_name} | ${runDetail.summary.graph_ref || "-"}`,
      });
    } catch (error) {
      setDetail(null);
      setStatus({ message: `Load failed: ${(error as Error).message}`, summary: "" });
    }
  }

  async function loadRuns(baseDir: string) {
    stopReplay();
    setStatus({ message: "Scanning replay cache...", summary: "" });

    try {
      const data = await api<RunsResponse>(buildUrl("/api/runs", baseDir));
      const runList = data.runs ?? [];
      setRuns(runList);
      setStatus({
        message: `Scanned ${data.cache_dir}`,
        summary: `${data.sources?.length ?? 0} sources | ${runList.length} runs`,
      });

      if (!runList.length) {
        if (modeRef.current !== "live") setDetail(null);
        return;
      }
      if (modeRef.current === "live") return;

      const preserved = runList.find(
        (item) => item.run.run_id === selectedRunId && item.source_id === selectedSourceId
      );
      const target = preserved ?? runList[0];
      await selectRun(baseDir, target);
    } catch (error) {
      setRuns([]);
      if (modeRef.current !== "live") setDetail(null);
      setStatus({ message: `Load failed: ${(error as Error).message}`, summary: "" });
    }
  }

  async function deleteRun(item: RunSummary) {
    if (!window.confirm(`确认删除 run ${item.run.run_id}？`)) {
      return;
    }

    try {
      setStatus({ message: `Deleting ${item.run.run_id}...`, summary: "" });
      await apiDelete<{ deleted: boolean }>(
        buildUrl(`/api/run/${encodeURIComponent(item.run.run_id)}`, DEFAULT_CACHE_DIR, {
          source: item.source_id,
        })
      );

      if (selectedRunId === item.run.run_id && selectedSourceId === item.source_id) {
        setDetail(null);
        setSelectedRunId("");
        setSelectedSourceId("");
        setReplayIndex(0);
      }

      await loadRuns(baseDir);
      setStatus({ message: `Deleted ${item.run.run_id}`, summary: "" });
    } catch (error) {
      setStatus({ message: `Delete failed: ${(error as Error).message}`, summary: "" });
    }
  }

  useEffect(() => {
    void loadRuns(DEFAULT_CACHE_DIR);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const current = detail?.replay?.[replayIndex] ?? null;

  return (
    <div className="relative h-full overflow-hidden bg-background text-foreground">
      {viewMode === "flow" ? (
        <ReplayGraphCanvas
          detail={detail}
          replayIndex={replayIndex}
          layoutVersion={layoutVersion}
        />
      ) : (
        <MermaidGraphCanvas detail={detail} replayIndex={replayIndex} />
      )}

      <div className="pointer-events-none absolute inset-0 z-20">
        <div className="flex h-full items-start justify-between gap-3">
          <ReplaySidebar
            cacheDir={DEFAULT_CACHE_DIR}
            detail={detail}
            runs={runs}
            selectedRunId={selectedRunId}
            selectedSourceId={selectedSourceId}
            replayIndex={replayIndex}
            isPlaying={isPlaying}
            statusMessage={status.message}
            isLiveMode={isLiveMode}
            liveState={liveState}
            liveSocketState={liveSocketState}
            liveBadge={liveBadge}
            liveDuration={liveDuration}
            viewMode={viewMode}
            sidebarCollapsed={sidebarCollapsed}
            sidebarWidth={sidebarWidth}
            liveEventsListRef={liveEventsListRef}
            onExpandSidebar={() => setSidebarCollapsed(false)}
            onCollapseSidebar={() => setSidebarCollapsed(true)}
            onToggleLiveMode={() => navigate(isLiveMode ? "/debug/replay" : "/debug/live")}
            onToggleViewMode={() => setViewMode((mode) => (mode === "flow" ? "mermaid" : "flow"))}
            onRelayout={() => setLayoutVersion((value) => value + 1)}
            onRefreshRuns={() => void loadRuns(DEFAULT_CACHE_DIR)}
            onSelectRun={(item) => void selectRun(DEFAULT_CACHE_DIR, item)}
            onDeleteRun={(item) => void deleteRun(item)}
            onReplayIndexChange={handleReplayIndex}
            onToggleReplay={toggleReplay}
            onSelectLiveEvent={setReplayIndex}
            onResizePointerDown={onResizePointerDown}
            onResizePointerMove={onResizePointerMove}
            onResizePointerUp={onResizePointerUp}
          />

          {!isLiveMode ? <ReplayPayloadPanel payload={current?.event.payload} /> : null}
        </div>
      </div>
    </div>
  );
}

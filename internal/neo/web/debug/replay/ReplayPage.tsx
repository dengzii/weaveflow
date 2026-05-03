import { useState, useEffect, useCallback, useRef } from "react";
import type {
  RunSummary,
  RunDetail,
  RunsResponse,
} from "./types";
import { api, buildUrl } from "./api";
import { RunCard } from "./components/RunCard";
import { OverviewSection } from "./components/OverviewSection";
import { ReplaySection } from "./components/ReplaySection";
import { StepsSection } from "./components/StepsSection";
import { CheckpointsPanel } from "./components/CheckpointsPanel";
import { ArtifactsPanel } from "./components/ArtifactsPanel";
import { SourceSection } from "./components/SourceSection";
import { Link } from "react-router-dom";
import { Button } from "../../components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../../components/ui/select";
import { RefreshCw, Database, ArrowLeft, Sparkles } from "lucide-react";

const DEFAULT_CACHE_DIR =
  (document.body.dataset.defaultCacheDir as string | undefined)?.trim() || "neo_data";

function runOptionValue(item: RunSummary): string {
  return `${item.source_id}::${item.run.run_id}`;
}

function runOptionLabel(item: RunSummary): string {
  return `${item.source_name || item.source_id} | ${item.run.run_id}`;
}

export function ReplayPage() {
  const [status, setStatus] = useState({ message: "Preparing", summary: "" });
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [selectedRunId, setSelectedRunId] = useState("");
  const [selectedSourceId, setSelectedSourceId] = useState("");
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [replayIndex, setReplayIndex] = useState(0);
  const [isPlaying, setIsPlaying] = useState(false);

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

  const stopReplay = useCallback(() => {
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
    setIsPlaying(false);
  }, []);

  const handleReplayIndex = useCallback(
    (index: number) => {
      stopReplay();
      const len = replayLengthRef.current;
      if (!len) return;
      setReplayIndex(Math.max(0, Math.min(index, len - 1)));
    },
    [stopReplay]
  );

  const toggleReplay = useCallback(() => {
    if (timerRef.current) {
      stopReplay();
      return;
    }
    if (!replayLengthRef.current) return;
    setIsPlaying(true);
    timerRef.current = window.setInterval(() => {
      setReplayIndex((prev) => {
        if (!replayLengthRef.current || prev >= replayLengthRef.current - 1) {
          clearInterval(timerRef.current!);
          timerRef.current = null;
          setIsPlaying(false);
          return prev;
        }
        return prev + 1;
      });
    }, 900);
  }, [stopReplay]);

  const selectRun = useCallback(
    async (baseDir: string, item: RunSummary) => {
      stopReplay();
      setSelectedRunId(item.run.run_id);
      setSelectedSourceId(item.source_id);
      setReplayIndex(0);
      setStatus({ message: "Loading run detail...", summary: "" });
      try {
        const url = buildUrl(`/api/run/${encodeURIComponent(item.run.run_id)}`, baseDir, {
          source: item.source_id,
        });
        const runDetail = await api<RunDetail>(url);
        setDetail(runDetail);
        setStatus({
          message: `Loaded run ${item.run.run_id}`,
          summary: `${item.source_name} | ${item.graph_ref || "-"}`,
        });
      } catch (err) {
        setDetail(null);
        setStatus({ message: `Load failed: ${(err as Error).message}`, summary: "" });
      }
    },
    [stopReplay]
  );

  const loadRuns = useCallback(
    async (baseDir: string) => {
      stopReplay();
      setStatus({ message: "Scanning Neo runs...", summary: "" });
      try {
        const data = await api<RunsResponse>(buildUrl("/api/runs", baseDir));
        const runList = data.runs ?? [];
        setRuns(runList);
        setStatus({
          message: `Scanned ${data.cache_dir}`,
          summary: `${data.sources?.length ?? 0} sources | ${runList.length} runs`,
        });

        if (!runList.length) {
          setDetail(null);
          return;
        }

        const preserved = runList.find(
          (item) => item.run.run_id === selectedRunId && item.source_id === selectedSourceId
        );
        await selectRun(baseDir, preserved ?? runList[0]);
      } catch (err) {
        setRuns([]);
        setDetail(null);
        setStatus({ message: `Load failed: ${(err as Error).message}`, summary: "" });
      }
    },
    [selectedRunId, selectedSourceId, selectRun, stopReplay]
  );

  useEffect(() => {
    void loadRuns(DEFAULT_CACHE_DIR);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const selectedRunValue =
    selectedRunId && selectedSourceId ? `${selectedSourceId}::${selectedRunId}` : undefined;

  return (
    <div className="flex flex-col h-full">
      <header className="flex items-center gap-4 px-6 h-14 border-b border-border shrink-0">
        <Link to="/">
          <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0">
            <ArrowLeft className="h-4 w-4" />
          </Button>
        </Link>
        <Link to="/debug/replay/v2">
          <Button variant="outline" size="sm" className="h-8 gap-1.5 shrink-0">
            <Sparkles className="h-3.5 w-3.5" />
            V2
          </Button>
        </Link>
        <div className="flex items-center gap-2 text-sm font-semibold">
          <Database className="h-4 w-4 text-muted-foreground" />
          Graph Replay
        </div>
        <div className="flex items-center gap-2 flex-1 max-w-2xl">
          <Select
            value={selectedRunValue}
            onValueChange={(value) => {
              const target = runs.find((item) => runOptionValue(item) === value);
              if (target) {
                void selectRun(DEFAULT_CACHE_DIR, target);
              }
            }}
          >
            <SelectTrigger className="h-8 text-xs">
              <SelectValue placeholder="Select a Neo run" />
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
            size="sm"
            variant="outline"
            className="h-8 gap-1.5 shrink-0"
            onClick={() => {
              void loadRuns(DEFAULT_CACHE_DIR);
            }}
          >
            <RefreshCw className="h-3.5 w-3.5" />
            Refresh
          </Button>
        </div>
        <div className="text-xs text-muted-foreground ml-auto">
          {status.message}
          {status.summary && <span className="ml-2 text-foreground/60">{status.summary}</span>}
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        <aside className="w-64 shrink-0 border-r border-border flex flex-col bg-sidebar">
          <div className="flex items-center justify-between px-4 py-2.5 border-b border-sidebar-border">
            <span className="text-xs font-medium text-sidebar-foreground">Runs</span>
            <span className="text-xs bg-muted text-muted-foreground px-1.5 py-0.5 rounded-full">
              {runs.length}
            </span>
          </div>
          <div className="flex-1 overflow-y-auto p-2 space-y-1">
            {runs.length === 0 ? (
              <div className="text-xs text-muted-foreground text-center py-6">No run data</div>
            ) : (
              runs.map((item) => (
                <RunCard
                  key={`${item.source_id}:${item.run.run_id}`}
                  item={item}
                  isActive={
                    item.run.run_id === selectedRunId && item.source_id === selectedSourceId
                  }
                  onClick={() => {
                    void selectRun(DEFAULT_CACHE_DIR, item);
                  }}
                />
              ))
            )}
          </div>
        </aside>

        <section className="flex-1 overflow-y-auto p-4 space-y-4">
          {!detail ? (
            <div className="flex items-center justify-center h-full min-h-72">
              <div className="text-center">
                <Database className="h-8 w-8 text-muted-foreground mx-auto mb-3" />
                <p className="text-sm font-medium">No run selected</p>
                <p className="text-xs text-muted-foreground mt-1">
                  Select a Neo run from the dropdown above.
                </p>
              </div>
            </div>
          ) : (
            <>
              <OverviewSection detail={detail} />
              <ReplaySection
                detail={detail}
                replayIndex={replayIndex}
                isPlaying={isPlaying}
                onIndexChange={handleReplayIndex}
                onTogglePlay={toggleReplay}
              />
              <StepsSection detail={detail} />
              <div className="grid grid-cols-2 gap-4">
                <CheckpointsPanel
                  key={`${detail.run.run_id}-cp`}
                  detail={detail}
                  cacheDir={DEFAULT_CACHE_DIR}
                />
                <ArtifactsPanel
                  key={`${detail.run.run_id}-art`}
                  detail={detail}
                  cacheDir={DEFAULT_CACHE_DIR}
                />
              </div>
              <SourceSection detail={detail} />
            </>
          )}
        </section>
      </div>
    </div>
  );
}

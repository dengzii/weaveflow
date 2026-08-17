import type { ComponentType, ReactNode } from "react";
import {
  Braces,
  ChevronUp,
  GitBranch,
  LayoutDashboard,
  Loader2,
  Pause,
  Play,
  Save,
  Settings,
  Square,
} from "lucide-react";
import { Button } from "../../components/ui/button";
import { cn } from "../../lib/utils";
import { getBackendBaseUrl } from "../../lib/backend";
import type { GraphDefinition } from "../../types";

type StreamStatus = "connecting" | "connected" | "reconnecting" | "gap" | "failed" | "closed";
interface StreamDiagnostics {
  lastEventID: string;
  retryAttempt: number;
  retryDelayMS: number;
  lastErrorKind: string;
  lastError: string;
  receivedEvents: number;
  discardedFrames: number;
  receivedEventsPerSecond: number;
  discardedFramesPerSecond: number;
  selectedEventsPerSecond: number;
  unselectedEventsPerSecond: number;
  selectedEventRatio: number;
  handlingDurationMS: number;
}
type RunControlMode = "run" | "active" | "resume";

export function WorkbenchShell({
  streamStatus,
  streamDiagnostics,
  busy,
  saving,
  unsaved,
  definition,
  runControlMode,
  canResume,
  runControlsDisabled,
  children,
  runStatusPanel,
  runStatusVisible,
  hasRunStatus,
  onRun,
  onSave,
  onPause,
  onStop,
  onResume,
  onShowRegistry,
  onShowSettings,
  onReconnectEventStream,
  onToggleRunStatus,
}: {
  streamStatus: StreamStatus;
  streamDiagnostics: StreamDiagnostics;
  busy: boolean;
  saving: boolean;
  unsaved: boolean;
  definition: GraphDefinition | null;
  runControlMode: RunControlMode;
  canResume: boolean;
  runControlsDisabled: boolean;
  children: ReactNode;
  runStatusPanel?: ReactNode;
  runStatusVisible: boolean;
  hasRunStatus: boolean;
  onRun: () => void;
  onSave: () => void;
  onPause: () => void;
  onStop: () => void;
  onResume: () => void;
  onShowRegistry: () => void;
  onShowSettings: () => void;
  onReconnectEventStream: () => void;
  onToggleRunStatus: () => void;
}) {
  const backendBaseUrl = getBackendBaseUrl();

  return (
    <div className="flex h-screen min-h-0 bg-background text-foreground">
      <aside className="flex w-16 shrink-0 flex-col items-center border-r border-border bg-sidebar py-3">
        <div className="mb-4 flex h-9 w-9 items-center justify-center rounded-md bg-primary text-primary-foreground">
          <GitBranch className="h-4 w-4" />
        </div>
        <NavButton icon={LayoutDashboard} active onClick={() => undefined} label="Graph" />
        <div className="flex-1" />
        <NavButton icon={Settings} onClick={onShowSettings} label="Settings" />
      </aside>

      <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        <header className="flex h-14 items-center gap-3 border-b border-border bg-background px-4">
          <div id="graph-title-slot" className="min-w-0" />
          <div className="flex-1" />
          <Button
            variant="outline"
            size="sm"
            className={cn(unsaved && "border-emerald-500/40 bg-emerald-500/15 hover:bg-emerald-500/25")}
            onClick={onSave}
            disabled={busy || !definition}
            title="Save graph"
          >
            {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
            Save
          </Button>
          {runControlMode === "active" ? (
            <>
              <Button variant="outline" size="sm" onClick={onPause} disabled={runControlsDisabled || !hasRunStatus} title="Pause run">
                <Pause className="h-4 w-4" />
                Pause
              </Button>
              <Button variant="danger" size="sm" onClick={onStop} disabled={runControlsDisabled || !hasRunStatus} title="Stop run">
                <Square className="h-4 w-4" />
                Stop
              </Button>
            </>
          ) : runControlMode === "resume" ? (
            <>
              <Button size="sm" onClick={onResume} disabled={busy || !canResume} title="Resume paused run">
                {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
                Resume
              </Button>
              <Button variant="danger" size="sm" onClick={onStop} disabled={busy || !hasRunStatus} title="Stop run">
                <Square className="h-4 w-4" />
                Stop
              </Button>
            </>
          ) : (
            <Button size="sm" onClick={onRun} disabled={busy || !definition} title="Run graph">
              {busy && !saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
              Run
            </Button>
          )}
          <Button variant="outline" size="sm" onClick={onShowRegistry} title="View registry">
            <Braces className="h-4 w-4" />
            Registry
          </Button>
        </header>

        {streamStatus === "gap" ? (
          <div className="border-b border-amber-500/30 bg-amber-500/10 px-4 py-2 text-sm text-amber-800 dark:text-amber-200">
            Runtime event history has a gap. Persistent run data was refreshed; live-only LLM chunks may be incomplete.
          </div>
        ) : streamStatus === "failed" ? (
          <div className="flex items-center gap-3 border-b border-destructive/30 bg-destructive/10 px-4 py-2 text-sm text-destructive">
            <span className="min-w-0 flex-1">
              Runtime event stream unavailable. {streamErrorSummary(streamDiagnostics)}
            </span>
            <Button variant="outline" size="sm" className="shrink-0" onClick={onReconnectEventStream}>
              Retry now
            </Button>
          </div>
        ) : streamStatus === "reconnecting" || streamStatus === "closed" ? (
          <div className="flex items-center gap-3 border-b border-amber-500/30 bg-amber-500/10 px-4 py-2 text-sm text-amber-800 dark:text-amber-200">
            <span className="min-w-0 flex-1">
              Runtime event stream disconnected. {streamErrorSummary(streamDiagnostics)}
              {streamStatus === "reconnecting" ? ` ${retryBackoffSummary(streamDiagnostics.retryDelayMS)}` : ""}
            </span>
            <Button variant="outline" size="sm" className="shrink-0" onClick={onReconnectEventStream}>
              Retry now
            </Button>
          </div>
        ) : null}

        <section className="relative isolate min-h-0 flex-1 overflow-hidden">{children}</section>

        {runStatusVisible ? runStatusPanel : null}

        <footer className="flex h-9 items-center gap-3 border-t border-border bg-muted/40 px-4 text-xs text-muted-foreground">
          <div
            className="flex min-w-0 items-center gap-1.5"
            title={streamDiagnosticsTitle(streamStatus, streamDiagnostics, backendBaseUrl)}
            aria-label={streamDiagnosticsTitle(streamStatus, streamDiagnostics, backendBaseUrl)}
          >
            <span className={cn("h-2 w-2 shrink-0 rounded-full", streamStatusDotClass(streamStatus))} aria-hidden="true" />
            <span className="truncate">{backendBaseUrl}</span>
          </div>
          <span>{definition ? `${definition.nodes.length} nodes` : "invalid graph"}</span>
          <span>{definition?.edges?.length ?? 0} edges</span>
          {hasRunStatus ? (
            <button
              className="ml-auto inline-flex items-center gap-1 rounded px-1.5 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
              onClick={onToggleRunStatus}
              title={runStatusVisible ? "Hide run panel" : "Show run panel"}
            >
              <ChevronUp className={`h-3.5 w-3.5 transition-transform ${runStatusVisible ? "rotate-180" : ""}`} />
              Run
            </button>
          ) : null}
        </footer>
      </main>
    </div>
  );
}

function streamStatusLabel(status: StreamStatus): string {
  switch (status) {
    case "connected":
      return "Server connected";
    case "connecting":
      return "Server connecting";
    case "reconnecting":
      return "Server reconnecting";
    case "gap":
      return "Server event gap";
    case "failed":
      return "Server stream failed";
    case "closed":
      return "Server disconnected";
  }
}

function streamStatusDotClass(status: StreamStatus): string {
  switch (status) {
    case "connected":
      return "bg-emerald-600 dark:bg-emerald-300";
    case "connecting":
    case "reconnecting":
    case "gap":
      return "bg-amber-600 dark:bg-amber-300";
    case "failed":
    case "closed":
      return "bg-destructive";
  }
}

function streamDiagnosticsTitle(
  status: StreamStatus,
  diagnostics: StreamDiagnostics,
  backendBaseUrl: string
): string {
  const handledEvents = diagnostics.selectedEventsPerSecond + diagnostics.unselectedEventsPerSecond;
  const averageDuration = handledEvents > 0 ? diagnostics.handlingDurationMS / handledEvents : 0;
  const lines = [
    `${streamStatusLabel(status)}: ${backendBaseUrl}`,
    `Last event: ${diagnostics.lastEventID || "none"}`,
    `Received/discarded total: ${diagnostics.receivedEvents}/${diagnostics.discardedFrames}`,
    `Received/discarded per second: ${diagnostics.receivedEventsPerSecond}/${diagnostics.discardedFramesPerSecond}`,
    `Selected/unselected per second: ${diagnostics.selectedEventsPerSecond}/${diagnostics.unselectedEventsPerSecond}`,
    `Selected ratio: ${(diagnostics.selectedEventRatio * 100).toFixed(1)}%`,
    `Average handling: ${averageDuration.toFixed(3)} ms`,
  ];
  if (diagnostics.lastErrorKind || diagnostics.lastError) {
    lines.splice(2, 0, `Last error: ${streamErrorSummary(diagnostics)}`);
  }
  return lines.join("\n");
}

function streamErrorSummary(diagnostics: StreamDiagnostics): string {
  const kind = diagnostics.lastErrorKind.replaceAll("_", " ");
  if (kind && diagnostics.lastError) return `${kind}: ${diagnostics.lastError}.`;
  if (diagnostics.lastError) return `${diagnostics.lastError}.`;
  if (kind) return `${kind}.`;
  return "Connection is not available.";
}

function retryBackoffSummary(delayMS: number): string {
  if (delayMS <= 0) return "Reconnecting now.";
  const seconds = delayMS / 1_000;
  const displaySeconds = seconds < 10 ? seconds.toFixed(1) : Math.round(seconds).toString();
  return `Automatic retry backoff: ${displaySeconds} s.`;
}

function NavButton({
  icon: Icon,
  active = false,
  label,
  onClick,
}: {
  icon: ComponentType<{ className?: string }>;
  active?: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      className={cn(
        "mb-1 flex h-10 w-10 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
        active && "bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground"
      )}
      onClick={onClick}
      title={label}
      aria-label={label}
    >
      <Icon className="h-4 w-4" />
    </button>
  );
}

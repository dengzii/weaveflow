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
import type { WorkspaceTab } from "./constants";

type StreamStatus = "connecting" | "connected" | "reconnecting" | "closed";
type RunControlMode = "run" | "active" | "resume";

export function WorkbenchShell({
  tab,
  streamStatus,
  busy,
  saving,
  unsaved,
  definition,
  runControlMode,
  canResume,
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
  onToggleRunStatus,
  onTabChange,
}: {
  tab: WorkspaceTab;
  streamStatus: StreamStatus;
  busy: boolean;
  saving: boolean;
  unsaved: boolean;
  definition: GraphDefinition | null;
  runControlMode: RunControlMode;
  canResume: boolean;
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
  onToggleRunStatus: () => void;
  onTabChange: (tab: WorkspaceTab) => void;
}) {
  const backendBaseUrl = getBackendBaseUrl();

  return (
    <div className="flex h-screen min-h-0 bg-background text-foreground">
      <aside className="flex w-16 shrink-0 flex-col items-center border-r border-border bg-sidebar py-3">
        <div className="mb-4 flex h-9 w-9 items-center justify-center rounded-md bg-primary text-primary-foreground">
          <GitBranch className="h-4 w-4" />
        </div>
        <NavButton icon={LayoutDashboard} active={tab === "graph"} onClick={() => onTabChange("graph")} label="Graph" />
        <div className="flex-1" />
        <NavButton icon={Settings} active={tab === "settings"} onClick={() => onTabChange("settings")} label="Settings" />
      </aside>

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 items-center gap-3 border-b border-border bg-background px-4">
          {tab === "graph" ? (
            <div id="graph-title-slot" className="min-w-0" />
          ) : (
            <div className="min-w-0">
              <div className="flex min-w-0 items-center gap-2">
                <span className="truncate text-sm font-semibold">WeaveFlow</span>
              </div>
            </div>
          )}
          <div className="flex-1" />
          {tab === "graph" ? (
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
          ) : null}
          {tab === "graph" && runControlMode === "active" ? (
            <>
              <Button variant="outline" size="sm" onClick={onPause} disabled={!hasRunStatus} title="Pause run">
                <Pause className="h-4 w-4" />
                Pause
              </Button>
              <Button variant="danger" size="sm" onClick={onStop} disabled={!hasRunStatus} title="Stop run">
                <Square className="h-4 w-4" />
                Stop
              </Button>
            </>
          ) : tab === "graph" && runControlMode === "resume" ? (
            <>
              <Button size="sm" onClick={onResume} disabled={busy || !canResume} title="Resume paused run">
                {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
                Resume
              </Button>
              <Button variant="danger" size="sm" onClick={onStop} disabled={!hasRunStatus} title="Stop run">
                <Square className="h-4 w-4" />
                Stop
              </Button>
            </>
          ) : tab === "graph" ? (
            <Button size="sm" onClick={onRun} disabled={busy || !definition} title="Run graph">
              {busy && !saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
              Run
            </Button>
          ) : null}
          <Button variant="outline" size="sm" onClick={onShowRegistry} title="View registry">
            <Braces className="h-4 w-4" />
            Registry
          </Button>
        </header>

        {streamStatus === "reconnecting" || streamStatus === "closed" ? (
          <div className="border-b border-amber-500/30 bg-amber-500/10 px-4 py-2 text-sm text-amber-800 dark:text-amber-200">
            Runtime event stream disconnected. Reconnecting automatically.
          </div>
        ) : null}

        <section className="min-h-0 flex-1">{children}</section>

        {runStatusVisible ? runStatusPanel : null}

        <footer className="flex h-9 items-center gap-3 border-t border-border bg-muted/40 px-4 text-xs text-muted-foreground">
          <div
            className="flex min-w-0 items-center gap-1.5"
            title={`${streamStatusLabel(streamStatus)}: ${backendBaseUrl}`}
            aria-label={`${streamStatusLabel(streamStatus)}: ${backendBaseUrl}`}
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
      return "bg-amber-600 dark:bg-amber-300";
    case "closed":
      return "bg-destructive";
  }
}

function NavButton({
  icon: Icon,
  active,
  label,
  onClick,
}: {
  icon: ComponentType<{ className?: string }>;
  active: boolean;
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

import type { ComponentType, ReactNode } from "react";
import {
  Activity,
  ChevronUp,
  Database,
  GitBranch,
  LayoutDashboard,
  Loader2,
  Play,
  RefreshCcw,
  Settings,
  Upload,
} from "lucide-react";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { cn } from "../../lib/utils";
import type { GraphDefinition, GraphInfo, RunRecord } from "../../types";
import type { WorkspaceTab } from "./constants";
import { statusTone } from "./utils";

export function WorkbenchShell({
  tab,
  graphInfo,
  selectedRun,
  status,
  busy,
  definition,
  runsCount,
  children,
  runStatusPanel,
  runStatusVisible,
  hasRunStatus,
  onRefresh,
  onRun,
  onToggleRunStatus,
  onTabChange,
  onUpload,
}: {
  tab: WorkspaceTab;
  graphInfo: GraphInfo | null;
  selectedRun: RunRecord | null;
  status: string;
  busy: boolean;
  definition: GraphDefinition | null;
  runsCount: number;
  children: ReactNode;
  runStatusPanel?: ReactNode;
  runStatusVisible: boolean;
  hasRunStatus: boolean;
  onRefresh: () => void;
  onRun: () => void;
  onToggleRunStatus: () => void;
  onTabChange: (tab: WorkspaceTab) => void;
  onUpload: () => void;
}) {
  return (
    <div className="flex h-screen min-h-0 bg-background text-foreground">
      <aside className="flex w-16 shrink-0 flex-col items-center border-r border-border bg-sidebar py-3">
        <div className="mb-4 flex h-9 w-9 items-center justify-center rounded-md bg-primary text-primary-foreground">
          <GitBranch className="h-4 w-4" />
        </div>
        <NavButton icon={LayoutDashboard} active={tab === "graph"} onClick={() => onTabChange("graph")} label="Graph" />
        <NavButton icon={Play} active={tab === "runs"} onClick={() => onTabChange("runs")} label="Runs" />
        <NavButton icon={Activity} active={tab === "observe"} onClick={() => onTabChange("observe")} label="Observe" />
        <NavButton icon={Database} active={tab === "manage"} onClick={() => onTabChange("manage")} label="Manage" />
        <div className="flex-1" />
        <NavButton icon={Settings} active={tab === "settings"} onClick={() => onTabChange("settings")} label="Settings" />
      </aside>

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 items-center gap-3 border-b border-border bg-background px-4">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="truncate text-sm font-semibold">WeaveFlow Debug</span>
              <Badge tone={graphInfo ? "ok" : "warn"}>{graphInfo ? graphInfo.id : "no graph"}</Badge>
              {selectedRun ? <Badge tone={statusTone(selectedRun.status)}>{selectedRun.status}</Badge> : null}
            </div>
            <div className="truncate text-xs text-muted-foreground">{status}</div>
          </div>
          <div className="flex-1" />
          <Button variant="outline" size="sm" onClick={onRefresh} disabled={busy} title="Refresh">
            <RefreshCcw className="h-4 w-4" />
            Refresh
          </Button>
          <Button variant="outline" size="sm" onClick={onUpload} disabled={busy || !definition} title="Upload graph">
            <Upload className="h-4 w-4" />
            Upload
          </Button>
          <Button size="sm" onClick={onRun} disabled={busy || !definition} title="Run graph">
            {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
            Run
          </Button>
        </header>

        <section className="min-h-0 flex-1">{children}</section>

        {runStatusVisible ? runStatusPanel : null}

        <footer className="flex h-9 items-center gap-3 border-t border-border bg-muted/40 px-4 text-xs text-muted-foreground">
          <span>{definition ? `${definition.nodes.length} nodes` : "invalid graph"}</span>
          <span>{definition?.edges?.length ?? 0} edges</span>
          <span>{runsCount} runs</span>
          <span className="truncate">server API proxied at root</span>
          {hasRunStatus ? (
            <button
              className="ml-auto inline-flex items-center gap-1 rounded px-1.5 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
              onClick={onToggleRunStatus}
              title={runStatusVisible ? "Hide run status" : "Show run status"}
            >
              <ChevronUp className={`h-3.5 w-3.5 transition-transform ${runStatusVisible ? "rotate-180" : ""}`} />
              Run status
            </button>
          ) : null}
        </footer>
      </main>
    </div>
  );
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

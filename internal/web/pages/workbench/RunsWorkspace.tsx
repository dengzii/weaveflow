import { Activity, Pause, Play, RefreshCcw, Square } from "lucide-react";
import { Button } from "../../components/ui/button";
import { cn, formatTime } from "../../lib/utils";
import type { RunRecord, StepRecord } from "../../types";
import { PanelHeader, StatusText } from "./shared";
import { statusTone } from "./utils";

export function RunsWorkspace({
  runs,
  selectedRunId,
  steps,
  onSelectRun,
  onRefresh,
  onPause,
  onCancel,
  onResume,
  onRerun,
  canResume,
  busy,
}: {
  runs: RunRecord[];
  selectedRunId: string;
  steps: StepRecord[];
  onSelectRun: (id: string) => void;
  onRefresh: () => void;
  onPause: () => void;
  onCancel: () => void;
  onResume: () => void;
  onRerun: () => void;
  canResume: boolean;
  busy: boolean;
}) {
  return (
    <div className="grid h-full min-h-0 grid-cols-[380px_minmax(0,1fr)]">
      <section className="flex min-h-0 flex-col border-r border-border bg-panel">
        <PanelHeader icon={Play} title="Runs" />
        <div className="flex flex-wrap items-center gap-2 border-b border-border p-3">
          <Button variant="outline" size="sm" onClick={() => void onRefresh()} title="Refresh runs">
            <RefreshCcw className="h-4 w-4" />
            Refresh
          </Button>
          <Button variant="outline" size="sm" onClick={onPause} disabled={!selectedRunId} title="Pause run">
            <Pause className="h-4 w-4" />
            Pause
          </Button>
          <Button variant="outline" size="sm" onClick={onResume} disabled={busy || !canResume} title="Resume paused run">
            <Play className="h-4 w-4" />
            Resume
          </Button>
          <Button variant="outline" size="sm" onClick={onRerun} disabled={busy || !selectedRunId} title="Rerun with current initial state">
            <Play className="h-4 w-4" />
            Rerun
          </Button>
          <Button variant="danger" size="sm" onClick={onCancel} disabled={!selectedRunId} title="Cancel run">
            <Square className="h-4 w-4" />
            Cancel
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          {runs.map((run) => (
            <button
              key={run.run_id}
              className={cn(
                "grid w-full gap-1 border-b border-border px-3 py-3 text-left hover:bg-accent",
                selectedRunId === run.run_id && "bg-accent"
              )}
              onClick={() => onSelectRun(run.run_id)}
            >
              <div className="flex items-center gap-2">
                <StatusText tone={statusTone(run.status)} className="shrink-0">{run.status}</StatusText>
                <span className="truncate text-sm font-medium">{run.run_id}</span>
              </div>
              <div className="text-xs text-muted-foreground">{formatTime(run.started_at)} / {run.graph_id}</div>
            </button>
          ))}
        </div>
      </section>
      <section className="min-h-0 overflow-auto bg-background p-4">
        <PanelHeader icon={Activity} title="Steps" inline />
        <div className="mt-3 overflow-hidden rounded-md border border-border">
          <table className="w-full table-fixed text-sm">
            <thead className="bg-muted text-xs text-muted-foreground">
              <tr>
                <th className="w-28 px-3 py-2 text-left">Status</th>
                <th className="px-3 py-2 text-left">Node</th>
                <th className="w-20 px-3 py-2 text-left">Attempt</th>
                <th className="w-28 px-3 py-2 text-left">Updated</th>
              </tr>
            </thead>
            <tbody>
              {steps.map((step) => (
                <tr key={step.step_id} className="border-t border-border">
                  <td className="px-3 py-2"><StatusText tone={statusTone(step.status)}>{step.status}</StatusText></td>
                  <td className="truncate px-3 py-2">{step.node_name || step.node_id}</td>
                  <td className="px-3 py-2 text-muted-foreground">{step.attempt}</td>
                  <td className="px-3 py-2 text-muted-foreground">{formatTime(step.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

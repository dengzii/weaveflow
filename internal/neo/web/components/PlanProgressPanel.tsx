import { AlertTriangle, CheckCircle2, Circle, ListChecks, Loader2, RotateCw } from "lucide-react";
import type { PlanProgress, PlanProgressStep } from "../types";
import { cn } from "../lib/utils";

interface Props {
  progress: PlanProgress | null;
}

function statusIcon(step: PlanProgressStep) {
  switch (step.status) {
    case "completed":
      return <CheckCircle2 className="h-3.5 w-3.5 text-green-600" />;
    case "in_progress":
      return <Loader2 className="h-3.5 w-3.5 animate-spin text-primary" />;
    case "blocked":
      return <AlertTriangle className="h-3.5 w-3.5 text-destructive" />;
    case "ready":
      return <Circle className="h-3.5 w-3.5 text-blue-500" />;
    default:
      return <Circle className="h-3.5 w-3.5 text-muted-foreground/55" />;
  }
}

function statusClass(status: string) {
  switch (status) {
    case "completed":
      return "text-muted-foreground line-through decoration-muted-foreground/40";
    case "in_progress":
      return "text-foreground";
    case "blocked":
      return "text-destructive";
    default:
      return "text-muted-foreground";
  }
}

function phaseLabel(progress: PlanProgress): string {
  if (progress.phase === "replanning" || progress.phase === "replanned") {
    return "Replan";
  }
  if (progress.status === "completed") {
    return "Complete";
  }
  if (progress.status === "blocked") {
    return "Blocked";
  }
  return "Plan";
}

export function PlanProgressPanel({ progress }: Props) {
  if (!progress || progress.counts.total === 0) {
    return null;
  }

  const currentTitle = progress.current_step?.title || "";
  const summary = progress.summary || progress.message || progress.replan_reason || progress.objective;

  return (
    <section className="chat-content px-6 pt-3">
      <div className="rounded-lg border border-border bg-card px-3.5 py-3 shadow-sm">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
              {progress.phase === "replanning"
                ? <RotateCw className="h-3.5 w-3.5" />
                : <ListChecks className="h-3.5 w-3.5" />}
              <span>{phaseLabel(progress)}</span>
              <span className="tabular-nums">
                {progress.counts.completed}/{progress.counts.total}
              </span>
            </div>
            {summary && (
              <div className="mt-1 truncate text-sm text-foreground">{summary}</div>
            )}
            {currentTitle && progress.status !== "completed" && (
              <div className="mt-1 truncate text-xs text-muted-foreground">
                Current: {currentTitle}
              </div>
            )}
          </div>
          <div className="shrink-0 text-right">
            <div className="text-sm font-semibold tabular-nums">{progress.percent}%</div>
            <div className="text-[11px] text-muted-foreground">{progress.status || "running"}</div>
          </div>
        </div>

        <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-muted">
          <div
            className={cn(
              "h-full rounded-full transition-all",
              progress.status === "blocked" ? "bg-destructive" : "bg-primary"
            )}
            style={{ width: `${progress.percent}%` }}
          />
        </div>

        <div className="mt-3 grid gap-1.5">
          {progress.steps.map((step) => (
            <div key={step.id} className="grid grid-cols-[18px_minmax(0,1fr)_auto] items-center gap-2 text-xs">
              {statusIcon(step)}
              <div className={cn("min-w-0 truncate", statusClass(step.status))}>
                {step.title || step.id}
              </div>
              <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                {step.status}
              </span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

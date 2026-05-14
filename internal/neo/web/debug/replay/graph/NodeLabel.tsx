import type {
  GraphNodeMeta,
  GraphProjection,
  NodeArtifactRef,
  NodeEventSummary,
  SourceGraph,
} from "./types";
import { SYNTHETIC_END_ID, SYNTHETIC_START_ID } from "./types";
import { formatNodeDuration, hasTokenUsageMetrics } from "./utils";
import { CollapsibleConfig } from "./CollapsibleConfig";
import { CollapsibleSection } from "./CollapsibleSection";
import { StatePatchSection } from "./StatePatchSection";
import { StateArtifactValuesSection } from "./StateArtifactValuesSection";
import { ArtifactToggleView } from "./ArtifactToggleView";
import { TokenUsageSection } from "./TokenUsageSection";

export function buildNodeLabel(
  node: GraphNodeMeta,
  sourceGraph: SourceGraph,
  projection: GraphProjection,
  summary?: NodeEventSummary,
  runId = "",
  sourceId = "",
  cacheDir = ""
) {
  const isCurrent = projection.currentNodeId === node.id;
  const isFailed = projection.failedNodeIds.has(node.id);
  const isCompleted = projection.completedNodeIds.has(node.id);
  const isVisited = projection.visitedNodeIds.has(node.id);
  const isStart = node.id === SYNTHETIC_START_ID || node.type === "start";
  const isEnd = node.id === SYNTHETIC_END_ID || node.type === "end";
  const statusLabel = isCurrent
    ? "LIVE"
    : isStart
      ? "START"
      : isEnd
        ? "END"
    : isFailed
      ? "FAILED"
      : isCompleted
        ? "DONE"
        : isVisited
          ? "SEEN"
          : "IDLE";
  const statusClass = isCurrent
    ? "border border-amber-500/40 bg-amber-500/12 text-amber-300"
    : isStart
      ? "border border-cyan-500/35 bg-cyan-500/10 text-cyan-300"
      : isEnd
        ? "border border-fuchsia-500/40 bg-fuchsia-500/12 text-fuchsia-300"
    : isFailed
      ? "border border-rose-500/35 bg-rose-500/10 text-rose-300"
      : isCompleted
        ? "border border-emerald-500/35 bg-emerald-500/10 text-emerald-300"
        : isVisited
          ? "border border-sky-500/35 bg-sky-500/10 text-sky-300"
          : "border border-slate-600/50 bg-slate-700/50 text-slate-400";
  if (isStart || isEnd) {
    return (
      <div className="text-center text-[11px] font-semibold uppercase tracking-[0.16em] text-slate-400">
        {node.name}
      </div>
    );
  }

  const durationLabel = summary && summary.durationMs >= 0 ? formatNodeDuration(summary.durationMs) : "";
  const hasTokenUsage = hasTokenUsageMetrics(summary);
  const contractArtifacts = summary
    ? [
        summary.contractInput
          ? { artifact: summary.contractInput, label: "Input", labelClassName: "text-cyan-400" }
          : null,
        summary.contractOutputPatch
          ? {
              artifact: summary.contractOutputPatch,
              label: "Output",
              labelClassName: "text-amber-400",
            }
          : null,
        summary.contractMergedState
          ? {
              artifact: summary.contractMergedState,
              label: "State",
              labelClassName: "text-emerald-400",
            }
          : null,
      ].filter(
        (
          item
        ): item is {
          artifact: NodeArtifactRef;
          label: string;
          labelClassName: string;
        } => Boolean(item)
      )
    : [];
  const hasStatePatch = Boolean(
    summary && (summary.statePatch.eventCount > 0 || summary.statePatch.changeCount > 0)
  );

  const hasEvents = Boolean(summary && (
    summary.llmReasoning ||
    summary.llmContent ||
    hasTokenUsage ||
    summary.functionCalls.length > 0 ||
    summary.toolCalls.length > 0 ||
    hasStatePatch ||
    contractArtifacts.length > 0 ||
    summary.artifacts.length > 0
  ));

  const configEntries = node.config
    ? Object.entries(node.config).filter(([, v]) => v !== null && v !== undefined && v !== "")
    : [];

  return (
    <div className="min-w-[200px] space-y-1.5 text-left">
      {/* Name + description tooltip + status */}
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-1.5">
          <div className="truncate text-[12px] font-semibold tracking-tight text-slate-100">{node.name}</div>
          {node.description ? (
            <span className="group/tip relative shrink-0 cursor-default select-none">
              <span className="inline-flex h-3.5 w-3.5 items-center justify-center rounded-full bg-slate-700/80 text-[8px] font-bold leading-none text-slate-400 ring-1 ring-slate-600/60 transition-all group-hover/tip:bg-sky-900/60 group-hover/tip:text-sky-400 group-hover/tip:ring-sky-600/50">
                i
              </span>
              <span className="pointer-events-none absolute bottom-full left-1/2 z-50 mb-1.5 -translate-x-1/2 whitespace-nowrap rounded-md bg-slate-800 px-2.5 py-1.5 text-[11px] leading-snug text-slate-200 opacity-0 shadow-xl ring-1 ring-slate-700 transition-opacity group-hover/tip:opacity-100">
                {node.description}
              </span>
            </span>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {durationLabel ? (
            <span className="text-[8px] tabular-nums text-slate-400">{durationLabel}</span>
          ) : null}
          <span className={`rounded-full px-2 py-0.5 text-[8px] font-semibold uppercase tracking-[0.14em] ${statusClass}`}>
            {statusLabel}
          </span>
        </div>
      </div>

      {/* Config key-value pairs */}
      {configEntries.length > 0 ? (
        <CollapsibleConfig entries={configEntries} />
      ) : null}

      {/* Content area — only rendered when events exist */}
      {hasEvents ? (
        <>
          <div className="h-px w-full bg-slate-700" />
          <div className="space-y-1.5">
            {hasTokenUsage ? (
              <TokenUsageSection usage={summary!.tokenUsage} />
            ) : null}
            {summary!.llmReasoning ? (
              <CollapsibleSection
                label="Reasoning"
                text={summary!.llmReasoning}
                labelClass="text-violet-400"
                textClass="text-slate-300"
              />
            ) : null}
            {summary!.llmContent ? (
              <CollapsibleSection
                label="Response"
                text={summary!.llmContent}
                labelClass="text-sky-400"
                textClass="text-slate-300"
              />
            ) : null}
            {(summary!.functionCalls.length > 0 || summary!.toolCalls.length > 0) ? (
              <div className="flex flex-wrap gap-1.5 [&_span]:rounded-full [&_span]:px-2 [&_span]:py-0.5">
                {summary!.functionCalls.slice(0, 3).map((fc, i) => (
                  <span key={i} className="font-mono text-[9px] text-amber-400">⚡ {fc.name}</span>
                ))}
                {summary!.toolCalls.slice(0, 4).map((tc, i) => (
                  <span key={i} className={`rounded-full border px-2 py-0.5 font-mono text-[9px] ${tc.status === "done" ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-400" : tc.status === "failed" ? "border-rose-500/30 bg-rose-500/10 text-rose-400" : "border-slate-600/50 bg-slate-700/40 text-slate-400"}`}>
                    {tc.status === "done" ? "✓" : tc.status === "failed" ? "✗" : "·"} {tc.name}
                  </span>
                ))}
              </div>
            ) : null}
            {hasStatePatch ? (
              <StatePatchSection patch={summary!.statePatch} />
            ) : null}
            {contractArtifacts.length > 0 ? (
              runId ? (
                <div className="space-y-0.5">
                  {contractArtifacts.map(({ artifact, label, labelClassName }) => (
                    <StateArtifactValuesSection
                      key={artifact.id}
                      artifact={artifact}
                      cacheDir={cacheDir}
                      label={label}
                      labelClassName={labelClassName}
                      runId={runId}
                      sourceId={sourceId}
                    />
                  ))}
                </div>
              ) : (
                <div className="flex flex-wrap gap-1.5">
                  {contractArtifacts.map(({ artifact, label, labelClassName }) => (
                    <span
                      key={artifact.id}
                      className={`rounded-full border border-slate-600/50 bg-slate-800/70 px-2 py-0.5 font-mono text-[9px] ${labelClassName}`}
                    >
                      {label}
                    </span>
                  ))}
                </div>
              )
            ) : null}
            {summary!.artifacts.length > 0 ? (
              runId ? (
                <div className="space-y-0.5">
                  {summary!.artifacts.slice(0, 3).map((a) => (
                    <ArtifactToggleView
                      key={a.id}
                      artifact={a}
                      cacheDir={cacheDir}
                      runId={runId}
                      sourceId={sourceId}
                    />
                  ))}
                </div>
              ) : (
                <div className="flex flex-wrap gap-1.5">
                  {summary!.artifacts.slice(0, 3).map((a, i) => (
                    <span key={i} className="rounded-full border border-violet-500/30 bg-violet-500/10 px-2 py-0.5 font-mono text-[9px] text-violet-400">⬡ {a.type || "artifact"}</span>
                  ))}
                </div>
              )
            ) : null}
          </div>
        </>
      ) : null}
    </div>
  );
}

import type { GraphNodeMeta, NodeArtifactRef, NodeEventSummary } from "./types";
import { formatNodeDuration, hasTokenUsageMetrics } from "./utils";
import { CollapsibleConfig } from "./CollapsibleConfig";
import { CollapsibleSection } from "./CollapsibleSection";
import { StatePatchSection } from "./StatePatchSection";
import { StateArtifactValuesSection } from "./StateArtifactValuesSection";
import { ArtifactToggleView } from "./ArtifactToggleView";
import { TokenUsageSection } from "./TokenUsageSection";

export function NodeInfoPanel({
  node,
  summary,
  cacheDir = "",
  runId = "",
  sourceId = "",
}: {
  node: GraphNodeMeta;
  summary: NodeEventSummary | null | undefined;
  cacheDir?: string;
  runId?: string;
  sourceId?: string;
}) {
  const configEntries = node.config
    ? Object.entries(node.config).filter(([, v]) => v !== null && v !== undefined && v !== "")
    : [];
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
  const hasContent =
    configEntries.length > 0 ||
    (summary?.durationMs !== undefined && summary.durationMs >= 0) ||
    hasTokenUsageMetrics(summary) ||
    summary?.llmReasoning ||
    summary?.llmContent ||
    (summary?.functionCalls.length ?? 0) > 0 ||
    (summary?.toolCalls.length ?? 0) > 0 ||
    hasStatePatch ||
    contractArtifacts.length > 0 ||
    (summary?.artifacts.length ?? 0) > 0;

  if (!hasContent) return null;

  return (
    <div className="space-y-2 text-left">
      {summary?.durationMs !== undefined && summary.durationMs >= 0 ? (
        <div className="flex items-center gap-1.5">
          <span className="text-[8px] uppercase tracking-[0.14em] text-slate-500">Duration</span>
          <span className="text-[10px] font-medium tabular-nums text-slate-300">{formatNodeDuration(summary.durationMs)}</span>
        </div>
      ) : null}
      {configEntries.length > 0 ? <CollapsibleConfig entries={configEntries} /> : null}
      {hasTokenUsageMetrics(summary) ? <TokenUsageSection usage={summary!.tokenUsage} /> : null}
      {summary?.llmReasoning ? (
        <CollapsibleSection
          label="Reasoning"
          text={summary.llmReasoning}
          labelClass="text-violet-400"
          textClass="text-slate-300"
        />
      ) : null}
      {summary?.llmContent ? (
        <CollapsibleSection
          label="Response"
          text={summary.llmContent}
          labelClass="text-sky-400"
          textClass="text-slate-300"
        />
      ) : null}
      {(summary?.functionCalls.length ?? 0) > 0 || (summary?.toolCalls.length ?? 0) > 0 ? (
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
      {hasStatePatch ? <StatePatchSection patch={summary!.statePatch} /> : null}
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
      {(summary?.artifacts.length ?? 0) > 0 ? (
        runId ? (
          <div className="space-y-0.5">
            {summary!.artifacts.map((a) => (
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
            {summary!.artifacts.map((a, i) => (
              <span key={i} className="rounded-full border border-violet-500/30 bg-violet-500/10 px-2 py-0.5 font-mono text-[9px] text-violet-400">⬡ {a.type || "artifact"}</span>
            ))}
          </div>
        )
      ) : null}
    </div>
  );
}

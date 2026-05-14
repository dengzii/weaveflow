import type { NodeEventSummary } from "./types";
import { formatTokenCount } from "./utils";

export function TokenUsageSection({
  usage,
}: {
  usage: NodeEventSummary["tokenUsage"];
}) {
  const items = [
    { label: "Total", value: usage.totalTokens, className: "border-cyan-500/30 bg-cyan-500/10 text-cyan-300" },
    { label: "Prompt", value: usage.promptTokens, className: "border-slate-500/30 bg-slate-800/70 text-slate-300" },
    { label: "Completion", value: usage.completionTokens, className: "border-sky-500/30 bg-sky-500/10 text-sky-300" },
    { label: "Reasoning", value: usage.reasoningTokens, className: "border-violet-500/30 bg-violet-500/10 text-violet-300" },
    { label: "Cached", value: usage.promptCachedTokens, className: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300" },
  ].filter((item) => item.value > 0);

  if (items.length === 0) return null;

  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <div className="text-[8px] font-semibold uppercase tracking-[0.12em] text-cyan-400">Tokens</div>
      <div className="flex flex-wrap items-center gap-1">
        {items.map((item) => (
          <span
            key={item.label}
            className={`inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 font-mono text-[9px] leading-none ${item.className}`}
          >
            <span className="opacity-70">{item.label}</span>
            <span>{formatTokenCount(item.value)}</span>
          </span>
        ))}
      </div>
    </div>
  );
}

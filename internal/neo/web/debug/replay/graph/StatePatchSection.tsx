import type { StatePatchSummary } from "./types";
import { CollapsibleSection } from "./CollapsibleSection";

export function StatePatchSection({ patch }: { patch: StatePatchSummary }) {
  if (patch.eventCount === 0 && patch.changeCount === 0) return null;

  const changeLabel = `${patch.changeCount} change${patch.changeCount === 1 ? "" : "s"}`;
  const patchLabel = patch.eventCount > 1 ? `${patch.eventCount} patches` : "1 patch";

  return (
    <div className="space-y-1">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-[8px] font-semibold uppercase tracking-[0.12em] text-emerald-400">
          State Patch
        </span>
        <span className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 font-mono text-[9px] text-emerald-300">
          {changeLabel}
        </span>
        <span className="rounded-full border border-slate-600/50 bg-slate-800/70 px-1.5 py-0.5 font-mono text-[9px] text-slate-300">
          {patchLabel}
        </span>
      </div>
      {patch.lastPaths.length > 0 ? (
        <CollapsibleSection
          label="Changed Paths"
          text={patch.lastPaths.join("\n")}
          labelClass="text-emerald-400"
          textClass="font-mono text-slate-300"
        />
      ) : null}
    </div>
  );
}

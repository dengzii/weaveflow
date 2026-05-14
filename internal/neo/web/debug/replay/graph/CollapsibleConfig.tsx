import { useState } from "react";
import { formatConfigValue, formatConfigValueFull } from "./utils";

const CONFIG_VALUE_THRESHOLD = 40;

export function CollapsibleConfig({ entries }: { entries: [string, unknown][] }) {
  return (
    <div className="space-y-0.5">
      {entries.map(([k, v]) => (
        <ConfigEntry key={k} k={k} v={v} />
      ))}
    </div>
  );
}

function ConfigEntry({ k, v }: { k: string; v: unknown }) {
  const [open, setOpen] = useState(false);
  const preview = formatConfigValue(v);
  const full = formatConfigValueFull(v);
  const isLong = full.length > CONFIG_VALUE_THRESHOLD;
  if (!isLong) {
    return (
      <div className="flex items-baseline justify-between gap-1.5 text-[9px]">
        <span className="shrink-0 font-medium text-slate-400">{k}</span>
        <span className="min-w-0 truncate text-right text-slate-300">{preview}</span>
      </div>
    );
  }
  return (
    <div className="text-[9px]">
      <button
        type="button"
        className="flex w-full items-baseline justify-between gap-1.5 text-left transition-colors hover:text-slate-100"
        onClick={(e) => { e.stopPropagation(); setOpen((o) => !o); }}
      >
        <span className="shrink-0 font-medium text-slate-400">{k}</span>
        <span className="flex min-w-0 items-baseline gap-1">
          {!open ? <span className="truncate text-slate-300">{preview}</span> : null}
          <span className="shrink-0 text-[8px] text-slate-500">{open ? "▴" : "▸"}</span>
        </span>
      </button>
      {open ? (
        <pre className="nodrag nowheel mt-1 max-h-[80px] overflow-auto whitespace-pre-wrap break-all font-mono leading-[1.5] text-slate-300">
          {full}
        </pre>
      ) : null}
    </div>
  );
}

import { useState } from "react";

export function JsonTree({ data, truncated }: { data: unknown; truncated?: boolean }) {
  return (
    <div className="nodrag nowheel select-text">
      <JsonTreeNode value={data} depth={0} />
      {truncated ? <div className="mt-0.5 text-[9px] italic text-slate-500">…truncated</div> : null}
    </div>
  );
}

function JsonTreeNode({ value, label, depth }: { value: unknown; label?: string; depth: number }) {
  const isArr = Array.isArray(value);
  const isObj = !isArr && value !== null && typeof value === "object";

  const keyPart = label !== undefined ? (
    <span className="shrink-0 text-sky-400">
      "{label}"<span className="text-slate-500">: </span>
    </span>
  ) : null;

  // Fixed-width chevron column so all rows in the same container share the same left edge for content.
  const chevronCol = "inline-block w-3 shrink-0";

  if (!isObj && !isArr) {
    return (
      <div className="flex min-w-0 gap-0.5">
        <span className={chevronCol} />
        {keyPart}
        <JsonPrimitive value={value} />
      </div>
    );
  }

  const entries: [string, unknown][] = isArr
    ? (value as unknown[]).map((v, i) => [String(i), v])
    : Object.entries(value as Record<string, unknown>);
  const [ob, cb] = isArr ? ["[", "]"] : ["{", "}"];
  const [open, setOpen] = useState(depth < 2 && entries.length <= 8);

  if (entries.length === 0) {
    return (
      <div className="flex min-w-0 gap-0.5">
        <span className={chevronCol} />
        {keyPart}
        <span className="text-slate-500">{ob}{cb}</span>
      </div>
    );
  }

  return (
    <div>
      <div
        className="flex min-w-0 cursor-pointer items-start gap-0.5 hover:opacity-75 transition-opacity"
        onClick={(e) => { e.stopPropagation(); setOpen((o) => !o); }}
      >
        <span className={`${chevronCol} mt-[3px] select-none text-[7px] text-slate-500`}>{open ? "▾" : "▸"}</span>
        {keyPart}
        <span className="text-slate-500">{ob}</span>
        {!open ? (
          <span className="text-[9px] text-slate-500">
            {isArr ? entries.length : `${entries.length} keys`}
          </span>
        ) : null}
        {!open ? <span className="text-slate-500">{cb}</span> : null}
      </div>
      {open ? (
        <>
          <div className="ml-3 border-l border-slate-700 pl-2">
            {entries.map(([k, v]) => (
              <JsonTreeNode key={k} value={v} label={isArr ? undefined : k} depth={depth + 1} />
            ))}
          </div>
          <div className="flex gap-0.5">
            <span className={chevronCol} />
            <span className="text-slate-500">{cb}</span>
          </div>
        </>
      ) : null}
    </div>
  );
}

function JsonPrimitive({ value }: { value: unknown }) {
  if (value === null) return <span className="text-slate-500">null</span>;
  if (typeof value === "boolean") return <span className="text-violet-400">{String(value)}</span>;
  if (typeof value === "number") return <span className="text-emerald-400">{String(value)}</span>;
  if (typeof value === "string") {
    const display = value.length > 80 ? `${value.slice(0, 80)}…` : value;
    return (
      <span className="text-amber-400" title={value.length > 80 ? value : undefined}>
        "{display}"
      </span>
    );
  }
  return <span className="text-slate-400">{String(value)}</span>;
}

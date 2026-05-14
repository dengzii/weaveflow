import { useState } from "react";

export function CollapsibleSection({
  label,
  text,
  labelClass,
  textClass,
}: {
  label: string;
  text: string;
  labelClass: string;
  textClass: string;
}) {
  const [open, setOpen] = useState(false);
  const chevron = open ? "▾" : "▸";
  return (
    <div className="grid grid-cols-[12px_minmax(0,1fr)] gap-x-1.5 text-left">
      <button
        type="button"
        className="col-span-2 grid w-full grid-cols-subgrid items-start py-0.5 text-left"
        onClick={(e) => { e.stopPropagation(); setOpen((o) => !o); }}
      >
        <span className="pt-[1px] text-[8px] text-slate-500">{chevron}</span>
        <span className={`min-w-0 text-[8px] font-semibold uppercase tracking-[0.12em] ${labelClass}`}>{label}</span>
      </button>
      {open ? (
        <div className={`nodrag nowheel col-start-2 mt-0.5 max-h-[130px] overflow-y-auto whitespace-pre-wrap break-words text-left text-[10px] leading-[1.55] ${textClass}`}>
          {text}
        </div>
      ) : (
        <p className={`col-start-2 mt-0.5 line-clamp-2 text-left text-[10px] leading-[1.55] ${textClass}`}>{text}</p>
      )}
    </div>
  );
}

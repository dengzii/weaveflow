import { useEffect, useId, useRef, useState, type ComponentType, type ReactNode } from "react";
import { Check, ChevronDown, ChevronRight } from "lucide-react";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { cn } from "../../../lib/utils";
import type { GraphNodeSpec } from "../../../types";

export function InputSuggestion({
  "aria-label": ariaLabel,
  value,
  options,
  placeholder,
  inputClassName,
  onChange,
}: {
  "aria-label": string;
  value: string;
  options: Array<{ value: string; detail?: string }>;
  placeholder?: string;
  inputClassName?: string;
  onChange: (value: string) => void;
}) {
  const listboxID = useId();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [open, setOpen] = useState(false);
  const hasOptions = options.length > 0;

  useEffect(() => {
    if (!open) return;

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (target && !containerRef.current?.contains(target)) setOpen(false);
    };

    document.addEventListener("pointerdown", handlePointerDown, true);
    return () => document.removeEventListener("pointerdown", handlePointerDown, true);
  }, [open]);

  return (
    <div ref={containerRef} className="relative min-w-0 flex-1">
      <Input
        role="combobox"
        aria-label={ariaLabel}
        aria-autocomplete="list"
        aria-controls={hasOptions ? listboxID : undefined}
        aria-expanded={open && hasOptions}
        value={value}
        placeholder={placeholder}
        className={cn("pr-9", inputClassName)}
        onFocus={() => setOpen(hasOptions)}
        onClick={() => setOpen(hasOptions)}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Escape") setOpen(false);
          if (event.key === "ArrowDown" && hasOptions) setOpen(true);
        }}
      />
      <button
        type="button"
        className="absolute right-1 top-1 inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
        aria-label={`Show ${ariaLabel} options`}
        aria-expanded={open && hasOptions}
        disabled={!hasOptions}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => setOpen((current) => hasOptions && !current)}
      >
        <ChevronDown className={cn("h-4 w-4 transition-transform", open && hasOptions && "rotate-180")} />
      </button>
      {open && hasOptions ? (
        <div
          id={listboxID}
          role="listbox"
          aria-label={`${ariaLabel} options`}
          className="absolute inset-x-0 top-full z-30 mt-1 grid max-h-64 min-w-0 gap-1 overflow-x-hidden overflow-y-auto rounded-md border border-border bg-background p-1 shadow-lg"
        >
          {options.map((option) => {
            const selected = option.value === value;
            return (
              <button
                key={option.value}
                type="button"
                role="option"
                aria-selected={selected}
                className={cn(
                  "grid min-w-0 grid-cols-[16px_minmax(0,1fr)] items-start gap-2 rounded-sm px-2 py-1.5 text-left text-xs hover:bg-accent",
                  selected && "bg-accent text-accent-foreground"
                )}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => {
                  onChange(option.value);
                  setOpen(false);
                }}
              >
                <Check className={cn("mt-0.5 h-3.5 w-3.5", selected ? "opacity-100" : "opacity-0")} />
                <span className="grid min-w-0 gap-0.5">
                  <span className="break-all font-mono font-medium">{option.value}</span>
                  {option.detail ? <span className="break-words text-[10px] text-muted-foreground">{option.detail}</span> : null}
                </span>
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

export function NodeSelect({
  value,
  nodes,
  disabled = false,
  className,
  onChange,
}: {
  value: string;
  nodes: GraphNodeSpec[];
  disabled?: boolean;
  className?: string;
  onChange: (value: string) => void;
}) {
  const labels = nodeSelectLabels(nodes);

  return (
    <Select value={value} onChange={(event) => onChange(event.target.value)} disabled={disabled} className={className}>
      <option value="">-</option>
      {nodes.map((node) => (
        <option key={node.id} value={node.id}>
          {labels.get(node.id)}
        </option>
      ))}
    </Select>
  );
}

export function nodeSelectLabels(nodes: GraphNodeSpec[]): Map<string, string> {
  const names = nodes.map((node) => node.name?.trim() ?? "");
  const nameCounts = new Map<string, number>();
  for (const name of names) {
    if (name) nameCounts.set(name, (nameCounts.get(name) ?? 0) + 1);
  }

  return new Map(
    nodes.map((node, index) => {
      const name = names[index];
      const label = name && (nameCounts.get(name) ?? 0) > 1 ? `${name} (${node.id})` : name || node.id;
      return [node.id, label];
    })
  );
}

export function InspectorBlock({
  title,
  action,
  children,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="grid min-w-0 gap-3 border-b border-border p-3 last:border-b-0">
      <div className="flex min-h-8 min-w-0 items-center gap-2">
        <div className="min-w-0 break-words text-xs font-semibold uppercase text-muted-foreground">{title}</div>
        {action ? <div className="ml-auto">{action}</div> : null}
      </div>
      {children}
    </section>
  );
}

export function CollapsibleInspectorBlock({
  title,
  open,
  onOpenChange,
  action,
  children,
}: {
  title: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  action?: ReactNode;
  children: ReactNode;
}) {
  const Icon = open ? ChevronDown : ChevronRight;
  return (
    <section className="border-b border-border last:border-b-0">
      <div className="flex min-h-11 items-center gap-2 bg-muted/40 px-3 hover:bg-muted/70">
        <button
          type="button"
          aria-expanded={open}
          onClick={() => onOpenChange(!open)}
          className="flex min-h-11 min-w-0 flex-1 items-center gap-2 text-left"
        >
          <Icon className="h-4 w-4 text-muted-foreground" />
          <span className="min-w-0 break-words text-sm font-semibold text-foreground">{title}</span>
        </button>
        {action ? <div className="shrink-0" onClick={(event) => event.stopPropagation()}>{action}</div> : null}
      </div>
      {open ? <div className="grid gap-3 p-3">{children}</div> : null}
    </section>
  );
}

export function Field({
  label,
  children,
  className,
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <label className={cn("grid min-w-0 gap-1 text-sm", className)}>
      <span className="text-xs font-medium text-foreground/80">{label}</span>
      {children}
    </label>
  );
}

export function PanelHeader({
  icon: Icon,
  title,
}: {
  icon: ComponentType<{ className?: string }>;
  title: string;
}) {
  return (
    <div className="flex min-h-11 min-w-0 shrink-0 items-center gap-2 border-b border-border px-3 py-2">
      <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
      <span className="min-w-0 break-words text-sm font-semibold">{title}</span>
    </div>
  );
}

export function InfoRows({ rows }: { rows: Array<[string, string]> }) {
  return (
    <div className="grid gap-1">
      {rows.map(([label, value]) => (
        <div key={label} className="grid grid-cols-[84px_minmax(0,1fr)] gap-2 text-xs">
          <span className="text-muted-foreground">{label}</span>
          <span className="min-w-0 break-all font-mono">{value || "-"}</span>
        </div>
      ))}
    </div>
  );
}

import type { ComponentType, ReactNode } from "react";
import { Select } from "../../../components/ui/select";
import { cn } from "../../../lib/utils";
import type { GraphNodeSpec } from "../../../types";

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
  return (
    <Select value={value} onChange={(event) => onChange(event.target.value)} disabled={disabled} className={className}>
      <option value="">-</option>
      {nodes.map((node) => (
        <option key={node.id} value={node.id}>
          {node.name || node.id}
        </option>
      ))}
    </Select>
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
    <section className="grid gap-3 border-b border-border p-3 last:border-b-0">
      <div className="flex min-h-8 items-center gap-2">
        <div className="text-xs font-semibold uppercase text-muted-foreground">{title}</div>
        {action ? <div className="ml-auto">{action}</div> : null}
      </div>
      {children}
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
    <label className={cn("grid gap-1 text-sm", className)}>
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
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
    <div className="flex h-11 shrink-0 items-center gap-2 border-b border-border px-3">
      <Icon className="h-4 w-4 text-muted-foreground" />
      <span className="text-sm font-semibold">{title}</span>
    </div>
  );
}

export function InfoRows({ rows }: { rows: Array<[string, string]> }) {
  return (
    <div className="grid gap-1">
      {rows.map(([label, value]) => (
        <div key={label} className="grid grid-cols-[84px_minmax(0,1fr)] gap-2 text-xs">
          <span className="text-muted-foreground">{label}</span>
          <span className="truncate font-mono">{value || "-"}</span>
        </div>
      ))}
    </div>
  );
}

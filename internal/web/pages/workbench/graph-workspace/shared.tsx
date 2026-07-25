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

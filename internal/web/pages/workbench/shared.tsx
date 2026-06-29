import type { ComponentType, ReactNode } from "react";
import { cn } from "../../lib/utils";

export function PanelHeader({
  icon: Icon,
  title,
  inline = false,
}: {
  icon: ComponentType<{ className?: string }>;
  title: string;
  inline?: boolean;
}) {
  return (
    <div className={cn("flex h-12 items-center gap-2 px-3", !inline && "border-b border-border")}>
      <Icon className="h-4 w-4 text-muted-foreground" />
      <span className="text-sm font-semibold">{title}</span>
    </div>
  );
}

export type StatusTone = "neutral" | "ok" | "warn" | "danger" | "live";

export function StatusText({
  children,
  tone = "neutral",
  className,
}: {
  children: ReactNode;
  tone?: StatusTone;
  className?: string;
}) {
  return <span className={cn("inline-block text-xs font-medium", statusTextClass(tone), className)}>{children}</span>;
}

export function statusTextClass(tone: StatusTone = "neutral"): string {
  switch (tone) {
    case "ok":
      return "text-emerald-700 dark:text-emerald-300";
    case "warn":
      return "text-amber-700 dark:text-amber-300";
    case "danger":
      return "text-destructive";
    case "live":
      return "text-cyan-700 dark:text-cyan-300";
    default:
      return "text-muted-foreground";
  }
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

export function ResourceColumn({
  title,
  icon,
  children,
}: {
  title: string;
  icon: ComponentType<{ className?: string }>;
  children: ReactNode;
}) {
  return (
    <section className="min-h-0 overflow-auto border-r border-border p-4 last:border-r-0">
      <PanelHeader icon={icon} title={title} inline />
      <div className="mt-3">{children}</div>
    </section>
  );
}

import type { InitialStateRequirement, InitialStateRequirements } from "../../../types";
import { StatusText, type StatusTone } from "../shared";

export function InitialStateRequirementList({
  requirements,
  showRequired = true,
}: {
  requirements: InitialStateRequirements | null;
  showRequired?: boolean;
}) {
  const required = showRequired ? (requirements?.required ?? []) : [];
  const providedByEntry = requirements?.provided_by_entry ?? [];
  const providedByUpstream = requirements?.provided_by_upstream ?? [];
  const unresolved = requirements?.unresolved ?? [];
  const warnings = requirements?.warnings ?? [];
  if (!requirements) {
    return <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">Requirements unavailable</div>;
  }
  if (required.length === 0 && providedByEntry.length === 0 && providedByUpstream.length === 0 && unresolved.length === 0 && warnings.length === 0) {
    return <div className="rounded-md border border-border bg-muted p-2 text-xs text-muted-foreground">No required initial state</div>;
  }
  return (
    <div className="grid gap-2">
      {required.length > 0 ? <RequirementGroup title="Required" tone="warn" items={required} /> : null}
      {unresolved.length > 0 ? <RequirementGroup title="Unresolved" tone="danger" items={unresolved} /> : null}
      {providedByEntry.length > 0 ? <RequirementGroup title="Provided by entry" tone="ok" items={providedByEntry} /> : null}
      {providedByUpstream.length > 0 ? <RequirementGroup title="Provided upstream" tone="ok" items={providedByUpstream} /> : null}
      {warnings.length > 0 ? (
        <div className="rounded-md border border-border bg-muted p-2">
          <div className="mb-2 flex items-center gap-2">
            <StatusText tone="warn">Warnings</StatusText>
            <span className="text-xs text-muted-foreground">{warnings.length}</span>
          </div>
          <div className="grid gap-1">
            {warnings.map((warning, index) => (
              <div key={`${warning.code ?? "warning"}-${index}`} className="text-xs text-muted-foreground">
                {warning.message ?? warning.code ?? "warning"}
              </div>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function RequirementGroup({
  title,
  tone,
  items,
}: {
  title: string;
  tone: StatusTone;
  items: InitialStateRequirement[];
}) {
  return (
    <div className="rounded-md border border-border bg-muted p-2">
      <div className="mb-2 flex items-center gap-2">
        <StatusText tone={tone}>{title}</StatusText>
        <span className="text-xs text-muted-foreground">{items.length}</span>
      </div>
      <div className="grid gap-1">
        {items.map((item) => (
          <div key={`${title}-${item.path}`} className="min-w-0 text-xs">
            <div className="break-all font-mono text-foreground">{item.path}</div>
            <div className="break-words text-muted-foreground">
              {[item.type, item.nodes?.length ? `nodes:${item.nodes.join(",")}` : "", item.sources?.length ? `sources:${item.sources.join(",")}` : ""]
                .filter(Boolean)
                .join(" / ")}
            </div>
            {item.message || item.description ? (
              <div className="line-clamp-2 text-muted-foreground">{item.message || item.description}</div>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  );
}

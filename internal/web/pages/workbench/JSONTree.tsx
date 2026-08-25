import { cn, stringifyJSON } from "../../lib/utils";

export function JSONTree({
  value,
  query = "",
  label,
  expandAll = false,
  scrollable = true,
}: {
  value: unknown;
  query?: string;
  label: string;
  expandAll?: boolean;
  scrollable?: boolean;
}) {
  return (
    <div
      role="tree"
      aria-label={label}
      className={cn(
        scrollable
          ? "h-full min-w-0 overflow-auto rounded border border-border bg-background p-2 font-mono text-[11px] [overflow-wrap:anywhere]"
          : "min-w-0 rounded border border-border bg-background p-2 font-mono text-[11px] [overflow-wrap:anywhere]"
      )}
    >
      <JSONTreeValue value={value} query={query.trim().toLowerCase()} depth={0} expandAll={expandAll} />
    </div>
  );
}

export function parseJSONTreeValue(value: unknown): unknown | null {
  if (value && typeof value === "object") return value;
  if (typeof value !== "string") return null;
  const candidate = value.trim();
  if (!candidate || !((candidate.startsWith("{") && candidate.endsWith("}")) || (candidate.startsWith("[") && candidate.endsWith("]")))) {
    return null;
  }
  try {
    const parsed = JSON.parse(candidate) as unknown;
    return parsed && typeof parsed === "object" ? parsed : null;
  } catch {
    return null;
  }
}

function JSONTreeValue({
  value,
  query,
  depth,
  expandAll,
}: {
  value: unknown;
  query: string;
  depth: number;
  expandAll: boolean;
}) {
  if (!value || typeof value !== "object") {
    return <span className="break-words text-foreground">{stringifyJSON(value)}</span>;
  }
  if (depth >= 8) {
    return <span className="break-words text-muted-foreground">{jsonTreeValuePreview(value)}</span>;
  }
  const entries = Array.isArray(value)
    ? value.map((item, index) => [String(index), item] as const)
    : Object.entries(value as Record<string, unknown>);
  const filteredEntries = query
    ? entries.filter(([key, item]) => key.toLowerCase().includes(query) || jsonTreeValueMatches(item, query))
    : entries;
  if (filteredEntries.length === 0) {
    return <span className="text-muted-foreground">{query ? "No matching values" : Array.isArray(value) ? "[]" : "{}"}</span>;
  }
  return (
    <div className="grid gap-0.5">
      {filteredEntries.map(([key, item]) => {
        const expandable = Boolean(item) && typeof item === "object";
        if (!expandable) {
          return (
            <div key={key} role="treeitem" className="grid grid-cols-[minmax(5rem,auto)_minmax(0,1fr)] gap-2 pl-1">
              <span className="break-all text-primary">{key}</span>
              <span className="break-words">{stringifyJSON(item)}</span>
            </div>
          );
        }
        return (
          <details key={key} open={Boolean(query) || expandAll} className="pl-1">
            <summary className="cursor-pointer break-all text-primary hover:text-foreground">
              {key} <span className="text-muted-foreground">{jsonTreeValuePreview(item)}</span>
            </summary>
            <div role="group" className="ml-3 border-l border-border pl-2">
              <JSONTreeValue value={item} query={query} depth={depth + 1} expandAll={expandAll} />
            </div>
          </details>
        );
      })}
    </div>
  );
}

function jsonTreeValueMatches(value: unknown, query: string): boolean {
  if (value === null || value === undefined) return String(value).includes(query);
  if (typeof value !== "object") return String(value).toLowerCase().includes(query);
  if (Array.isArray(value)) return value.some((item) => jsonTreeValueMatches(item, query));
  return Object.entries(value).some(
    ([key, item]) => key.toLowerCase().includes(query) || jsonTreeValueMatches(item, query)
  );
}

function jsonTreeValuePreview(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "string") return value.length > 120 ? `${value.slice(0, 117)}…` : value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return `Array(${value.length})`;
  if (value && typeof value === "object") return `Object(${Object.keys(value).length})`;
  return typeof value;
}

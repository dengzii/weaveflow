import type { NodeEventSummary } from "./types";

export function stringValue(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

export function objectValue(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" ? (value as Record<string, unknown>) : null;
}

export function uniqueStrings(values: string[]): string[] {
  const seen = new Set<string>();
  const unique: string[] = [];
  for (const value of values) {
    if (seen.has(value)) continue;
    seen.add(value);
    unique.push(value);
  }
  return unique;
}

export function metricValue(payload: Record<string, unknown>, key: string): number {
  const value = payload[key];
  if (typeof value === "number" && Number.isFinite(value)) {
    return Math.max(0, Math.round(value));
  }
  if (typeof value === "string") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) {
      return Math.max(0, Math.round(parsed));
    }
  }
  return 0;
}

export function formatConfigValue(v: unknown): string {
  if (typeof v === "string") return v.length > 60 ? `${v.slice(0, 60)}…` : v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (Array.isArray(v)) return `[${v.slice(0, 3).map(String).join(", ")}${v.length > 3 ? "…" : ""}]`;
  if (typeof v === "object" && v !== null) return JSON.stringify(v).slice(0, 60);
  return String(v ?? "");
}

export function formatConfigValueFull(v: unknown): string {
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (Array.isArray(v) || (typeof v === "object" && v !== null)) return JSON.stringify(v, null, 2);
  return String(v ?? "");
}

export function formatNodeDuration(ms: number): string {
  if (ms < 0) return "";
  if (ms < 1000) return "< 1s";
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  const m = Math.floor(ms / 60000);
  const s = Math.floor((ms % 60000) / 1000);
  return s > 0 ? `${m}m ${s}s` : `${m}m`;
}

export function formatTokenCount(value: number): string {
  if (value >= 1000) {
    return `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)}k`;
  }
  return `${value}`;
}

export function formatFuncArgs(raw: unknown): string {
  if (!raw) return "";
  let obj: Record<string, unknown> | null = null;
  if (typeof raw === "string") {
    try {
      obj = JSON.parse(raw) as Record<string, unknown>;
    } catch {
      return raw.slice(0, 30);
    }
  } else if (typeof raw === "object") {
    obj = raw as Record<string, unknown>;
  }
  if (!obj) return String(raw).slice(0, 30);
  return Object.entries(obj)
    .slice(0, 2)
    .map(([k, v]) => {
      const val = typeof v === "string" ? `"${v.slice(0, 16)}"` : String(v).slice(0, 16);
      return `${k}=${val}`;
    })
    .join(", ");
}

export function hasTokenUsageMetrics(summary: NodeEventSummary | null | undefined): boolean {
  if (!summary) return false;
  const usage = summary.tokenUsage;
  return Boolean(
    usage.promptTokens ||
      usage.completionTokens ||
      usage.totalTokens ||
      usage.reasoningTokens ||
      usage.promptCachedTokens
  );
}

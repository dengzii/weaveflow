export function formatTime(value: string | null | undefined): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", { hour12: false });
}

export function formatDuration(ms: number): string {
  if (!ms) return "0 ms";
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(2)} s`;
  if (ms < 3600000) return `${(ms / 60000).toFixed(2)} min`;
  return `${(ms / 3600000).toFixed(2)} h`;
}

export function formatBytes(bytes: number): string {
  if (!bytes) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

export function prettyJSON(value: unknown): string {
  if (value === undefined || value === null) return "";
  return JSON.stringify(value, null, 2);
}

export function statusVariant(
  status: string
): "default" | "secondary" | "destructive" | "outline" | "warning" | "success" {
  const v = String(status ?? "").toLowerCase();
  if (v.includes("failed") || v.includes("error")) return "destructive";
  if (v.includes("paused") || v.includes("warning")) return "warning";
  if (v.includes("completed") || v.includes("succeeded") || v.includes("success"))
    return "success";
  return "secondary";
}

export function statusDotClass(status: string): string {
  const v = String(status ?? "").toLowerCase();
  if (v.includes("failed") || v.includes("error") || v === "is-error") return "bg-destructive";
  if (v.includes("paused") || v.includes("warning") || v === "is-warning") return "bg-yellow-500";
  if (
    v.includes("completed") || v.includes("succeeded") || v.includes("success") || v === "is-success"
  )
    return "bg-green-500";
  return "bg-blue-500";
}

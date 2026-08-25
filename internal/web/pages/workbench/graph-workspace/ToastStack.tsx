import { useEffect } from "react";
import { AlertCircle, AlertTriangle, CheckCircle2, X } from "lucide-react";
import { cn } from "../../../lib/utils";

export type ToastTone = "info" | "warn" | "error";

export interface ToastRecord {
  id: string;
  tone: ToastTone;
  message: string;
  title?: string;
}

export function ToastStack({
  toasts,
  persistentNotices = [],
  onDismiss,
}: {
  toasts: ToastRecord[];
  persistentNotices?: ToastRecord[];
  onDismiss: (id: string) => void;
}) {
  useEffect(() => {
    if (toasts.length === 0) return;
    const timers = toasts.map((toast) =>
      window.setTimeout(
        () => onDismiss(toast.id),
        toast.tone === "error" ? 9000 : toast.tone === "warn" ? 7500 : 5500
      )
    );
    return () => {
      for (const timer of timers) window.clearTimeout(timer);
    };
  }, [onDismiss, toasts]);

  if (toasts.length === 0 && persistentNotices.length === 0) return null;

  const notices = [
    ...persistentNotices.map((notice) => ({ ...notice, persistent: true })),
    ...toasts.map((toast) => ({ ...toast, persistent: false })),
  ];

  return (
    <div
      className="pointer-events-none absolute right-4 top-4 z-50 grid max-h-[calc(100%-2rem)] w-[min(400px,calc(100%-2rem))] gap-3 overflow-y-auto"
      aria-label="Graph notifications"
    >
      {notices.map((notice) => {
        const Icon = notice.tone === "error" ? AlertCircle : notice.tone === "warn" ? AlertTriangle : CheckCircle2;
        const title = notice.title?.trim() || notificationTitle(notice.tone);
        return (
          <section
            key={`${notice.persistent ? "persistent" : "toast"}-${notice.id}`}
            role={notice.tone === "info" ? "status" : "alert"}
            aria-label={`${title} notification`}
            className={cn(
              "pointer-events-auto overflow-hidden rounded-lg border bg-panel/95 text-foreground shadow-xl backdrop-blur",
              notice.tone === "error" && "border-destructive/45",
              notice.tone === "warn" && "border-amber-500/45",
              notice.tone === "info" && "border-emerald-500/45"
            )}
          >
            <div
              className={cn(
                "flex min-h-9 items-center gap-2 border-b px-3 py-2",
                notice.tone === "error" && "border-destructive/25 bg-destructive/10 text-destructive",
                notice.tone === "warn" && "border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-300",
                notice.tone === "info" && "border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
              )}
            >
              <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
              <span className="min-w-0 flex-1 truncate text-xs font-semibold">{title}</span>
              {!notice.persistent ? (
                <button
                  type="button"
                  className="-mr-1 rounded p-1 opacity-70 transition-colors hover:bg-current/10 hover:opacity-100"
                  onClick={() => onDismiss(notice.id)}
                  aria-label={`Dismiss ${title.toLowerCase()}`}
                  title="Dismiss"
                >
                  <X className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              ) : null}
            </div>
            <div className="max-h-40 overflow-y-auto whitespace-pre-wrap break-words px-3 py-2.5 text-xs leading-5 text-foreground">
              {notice.message}
            </div>
          </section>
        );
      })}
    </div>
  );
}

function notificationTitle(tone: ToastTone): string {
  if (tone === "error") return "Error";
  if (tone === "warn") return "Warning";
  return "Notice";
}

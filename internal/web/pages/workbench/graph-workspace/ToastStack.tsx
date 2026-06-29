import { useEffect } from "react";
import { AlertCircle, CheckCircle2, Info, X } from "lucide-react";
import { cn } from "../../../lib/utils";

export type ToastTone = "info" | "warn" | "error";

export interface ToastRecord {
  id: string;
  tone: ToastTone;
  message: string;
}

export function ToastStack({
  toasts,
  onDismiss,
}: {
  toasts: ToastRecord[];
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

  if (toasts.length === 0) return null;

  return (
    <div className="pointer-events-none absolute right-4 top-4 z-50 grid w-[min(420px,calc(100%-2rem))] gap-2">
      {toasts.map((toast) => {
        const Icon = toast.tone === "error" ? AlertCircle : toast.tone === "warn" ? Info : CheckCircle2;
        return (
          <div
            key={toast.id}
            className={cn(
              "pointer-events-auto flex items-start gap-2 rounded-md border px-3 py-2 text-sm shadow-lg backdrop-blur-sm",
              toast.tone === "error" && "border-destructive/40 bg-destructive/10 text-destructive",
              toast.tone === "warn" && "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300",
              toast.tone === "info" && "border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
            )}
          >
            <Icon className="mt-0.5 h-4 w-4 shrink-0" />
            <span className="flex-1 whitespace-pre-wrap break-words">{toast.message}</span>
            <button
              className="rounded p-1 opacity-70 transition-colors hover:bg-current/10 hover:opacity-100"
              onClick={() => onDismiss(toast.id)}
              aria-label="Dismiss notification"
              title="Dismiss"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        );
      })}
    </div>
  );
}

import { useEffect } from "react";
import { AlertCircle, X } from "lucide-react";

export function ErrorToast({ message, onDismiss }: { message: string; onDismiss: () => void }) {
  useEffect(() => {
    if (!message) return;
    const timer = window.setTimeout(onDismiss, 8000);
    return () => window.clearTimeout(timer);
  }, [message, onDismiss]);

  if (!message) return null;

  return (
    <div className="pointer-events-none absolute right-4 top-4 z-50 max-w-md">
      <div className="pointer-events-auto flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive shadow-lg backdrop-blur-sm">
        <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
        <span className="flex-1 whitespace-pre-wrap break-words">{message}</span>
        <button
          className="-mr-1 -mt-1 rounded p-1 text-destructive/70 transition-colors hover:bg-destructive/15 hover:text-destructive"
          onClick={onDismiss}
          aria-label="Dismiss error"
          title="Dismiss"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}

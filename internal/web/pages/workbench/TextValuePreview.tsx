import { useEffect, useId, useState } from "react";
import { X } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Button } from "../../components/ui/button";
import { cn } from "../../lib/utils";
import { WorkbenchDialogOverlay } from "./shared";

export function TextValuePreview({
  value,
  label,
  className,
  multiline = false,
  displayValue = value,
}: {
  value: string;
  label: string;
  className?: string;
  multiline?: boolean;
  displayValue?: string;
}) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <button
        type="button"
        className={cn(
          "min-w-0 max-w-full cursor-pointer text-left font-mono hover:text-primary hover:underline hover:decoration-dotted hover:underline-offset-2",
          multiline ? "whitespace-pre-wrap break-words" : "truncate",
          className
        )}
        title={`Open ${label} as Markdown`}
        aria-label={`Open ${label} text`}
        aria-haspopup="dialog"
        onClick={() => setOpen(true)}
      >
        {displayValue}
      </button>
      {open ? <MarkdownTextDialog label={label} text={value} onClose={() => setOpen(false)} /> : null}
    </>
  );
}

export function MarkdownTextDialog({
  label,
  text,
  onClose,
}: {
  label: string;
  text: string;
  onClose: () => void;
}) {
  const titleID = useId();

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  return (
    <WorkbenchDialogOverlay onDismiss={onClose}>
      <div
        className="flex h-[min(720px,90vh)] w-[min(1000px,94vw)] min-w-0 flex-col rounded-md border border-border bg-panel shadow-xl"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleID}
      >
        <div className="flex h-14 shrink-0 items-center gap-2 border-b border-border px-4">
          <div id={titleID} className="min-w-0 truncate text-sm font-semibold" title={label}>
            {label}
          </div>
          <span className="text-[11px] text-muted-foreground">Markdown</span>
          <Button className="ml-auto" variant="ghost" size="icon" onClick={onClose} title="Close full text" aria-label="Close full text">
            <X className="h-4 w-4" />
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-auto bg-background p-5">
          <div className="detail-markdown text-sm leading-6">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown>
          </div>
        </div>
        <div className="shrink-0 border-t border-border px-4 py-2 text-right text-[11px] tabular-nums text-muted-foreground">
          {text.length.toLocaleString()} characters
        </div>
      </div>
    </WorkbenchDialogOverlay>
  );
}

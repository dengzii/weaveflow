import { useState } from "react";
import { HelpCircle } from "lucide-react";
import type { ClarificationQuestion } from "../types";
import { cn } from "../lib/utils";

interface Props {
  clarification: ClarificationQuestion | null;
  onSelect: (text: string) => void;
  disabled?: boolean;
}

export function ClarificationPanel({ clarification, onSelect, disabled }: Props) {
  const [custom, setCustom] = useState("");

  if (!clarification) {
    return null;
  }

  const submitCustom = () => {
    const text = custom.trim();
    if (!text || disabled) {
      return;
    }
    onSelect(text);
    setCustom("");
  };

  return (
    <section className="chat-content px-6 pt-3">
      <div className="rounded-lg border border-amber-300 bg-amber-50 px-3.5 py-3 shadow-sm dark:border-amber-700/60 dark:bg-amber-950/40">
        <div className="flex items-center gap-2 text-xs font-medium text-amber-700 dark:text-amber-300">
          <HelpCircle className="h-3.5 w-3.5" />
          <span>需要澄清</span>
          {typeof clarification.attempts === "number" && clarification.attempts > 0 && (
            <span className="tabular-nums">尝试 {clarification.attempts + 1}</span>
          )}
        </div>

        {clarification.question && (
          <div className="mt-1 text-sm text-foreground">{clarification.question}</div>
        )}

        {clarification.reasoning && (
          <div className="mt-1 text-xs text-muted-foreground">{clarification.reasoning}</div>
        )}

        {clarification.options.length > 0 && (
          <div className="mt-3 grid gap-1.5">
            {clarification.options.map((option, idx) => (
              <button
                key={`${idx}-${option}`}
                type="button"
                disabled={disabled}
                onClick={() => onSelect(option)}
                className={cn(
                  "rounded-md border border-border bg-card px-3 py-2 text-left text-xs text-foreground",
                  "transition-colors hover:bg-muted",
                  "disabled:cursor-not-allowed disabled:opacity-50",
                )}
              >
                {option}
              </button>
            ))}
          </div>
        )}

        <div className="mt-3 flex gap-2">
          <input
            type="text"
            value={custom}
            onChange={(event) => setCustom(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                submitCustom();
              }
            }}
            placeholder="或者输入你自己的澄清..."
            disabled={disabled}
            className={cn(
              "flex-1 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs",
              "placeholder:text-muted-foreground",
              "focus:outline-none focus:ring-1 focus:ring-primary",
              "disabled:cursor-not-allowed disabled:opacity-50",
            )}
          />
          <button
            type="button"
            disabled={disabled || custom.trim().length === 0}
            onClick={submitCustom}
            className={cn(
              "rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground",
              "transition-colors hover:bg-primary/90",
              "disabled:cursor-not-allowed disabled:opacity-50",
            )}
          >
            提交
          </button>
        </div>
      </div>
    </section>
  );
}

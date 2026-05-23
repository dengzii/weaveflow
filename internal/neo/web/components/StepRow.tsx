import { CheckIcon, Loader2 } from "lucide-react";
import { cn } from "../lib/utils";
import type { MessageItem } from "../types";

type Props = { item: Extract<MessageItem, { kind: "step" }> };

function doneText(text: string): string {
  return text.replace(/^姝ｅ湪/, "").replace(/^正在/, "").replace(/\.\.\.$/, "");
}

export function StepRow({ item }: Props) {
  const done = item.status === "done";
  return (
    <div className={cn(
      "text-xs py-0.5 pl-0.5 select-none",
      done ? "text-muted-foreground/60" : "text-muted-foreground"
    )}>
      <div className="flex items-center gap-2">
        {done
          ? <CheckIcon className="h-3 w-3 shrink-0 text-green-500/70" />
          : <Loader2 className="h-3 w-3 shrink-0 animate-spin text-primary/70" />
        }
        <span>{done ? doneText(item.text) : item.text}</span>
      </div>
      {!!item.details?.length && (
        <div className="ml-5 mt-1 flex flex-wrap gap-1">
          {item.details.map((detail) => (
            <span
              key={detail}
              className="max-w-full truncate rounded border border-border/40 bg-muted/30 px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground/70"
              title={detail}
            >
              {detail}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

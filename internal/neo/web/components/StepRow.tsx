import { CheckIcon, Loader2 } from "lucide-react";
import { cn } from "../lib/utils";
import type { MessageItem } from "../types";

type Props = { item: Extract<MessageItem, { kind: "step" }> };

function doneText(text: string): string {
  return text.replace(/^正在/, "").replace(/\.\.\.$/, "");
}

export function StepRow({ item }: Props) {
  const done = item.status === "done";
  return (
    <div className={cn(
      "flex items-center gap-2 text-xs py-0.5 pl-0.5 select-none",
      done ? "text-muted-foreground/60" : "text-muted-foreground"
    )}>
      {done
        ? <CheckIcon className="h-3 w-3 shrink-0 text-green-500/70" />
        : <Loader2 className="h-3 w-3 shrink-0 animate-spin text-primary/70" />
      }
      <span>{done ? doneText(item.text) : item.text}</span>
    </div>
  );
}

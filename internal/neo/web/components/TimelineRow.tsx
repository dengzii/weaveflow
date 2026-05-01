import type { TimelineEntry } from "../types";
import { cn } from "../lib/utils";

interface Props {
  entries: TimelineEntry[];
}

export function TimelineRow({ entries }: Props) {
  if (entries.length === 0) return null;

  return (
    <div className="flex justify-start w-full">
      <div className="max-w-[85%] flex flex-wrap gap-1.5 items-center">
        {entries.map((entry, i) => {
          if (entry.kind === "phase") {
            const isOk = entry.cls === "tl-ok";
            const isErr = entry.cls === "tl-err";
            return (
              <span
                key={i}
                className={cn(
                  "inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs border",
                  isOk && "bg-green-500/10 text-green-400 border-green-500/20",
                  isErr && "bg-destructive/10 text-destructive border-destructive/20",
                  !isOk && !isErr && "bg-muted text-muted-foreground border-border/50"
                )}
              >
                <span>{entry.icon}</span>
                <span>{entry.text}</span>
              </span>
            );
          }

          const isOk = entry.status === "ok";
          const isErr = entry.status === "err";
          const isPending = entry.status === "pending";

          return (
            <span
              key={entry.id}
              className={cn(
                "inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs border font-mono",
                isOk && "bg-green-500/10 text-green-400 border-green-500/20",
                isErr && "bg-destructive/10 text-destructive border-destructive/20",
                isPending && "bg-blue-500/10 text-blue-400 border-blue-500/20 animate-pulse"
              )}
            >
              <span>⚡</span>
              <span className="font-sans">{entry.name}</span>
              {!isPending && (
                <span className="opacity-70 max-w-[12ch] truncate">{entry.result}</span>
              )}
            </span>
          );
        })}
      </div>
    </div>
  );
}

import { useState } from "react";
import { CheckIcon, XIcon, Loader2, ChevronRightIcon, ZapIcon } from "lucide-react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./ui/collapsible";
import { cn } from "../lib/utils";
import type { MessageItem } from "../types";

type Props = { item: Extract<MessageItem, { kind: "tool" }> };

export function ToolRow({ item }: Props) {
  const [expanded, setExpanded] = useState(false);
  const calling = item.status === "calling";
  const isErr = item.status === "error";
  const hasDetail = !!item.args || !!item.output || !!item.error;

  return (
    <Collapsible open={expanded} onOpenChange={setExpanded}>
      <CollapsibleTrigger
        disabled={!hasDetail}
        className={cn(
          "flex items-center gap-2 text-xs py-0.5 pl-0.5 w-full text-left",
          "disabled:cursor-default",
          hasDetail && "hover:text-foreground transition-colors cursor-pointer"
        )}
      >
        {calling
          ? <Loader2 className="h-3 w-3 shrink-0 animate-spin text-primary/70" />
          : isErr
            ? <XIcon className="h-3 w-3 shrink-0 text-destructive/70" />
            : <CheckIcon className="h-3 w-3 shrink-0 text-green-500/70" />
        }
        <ZapIcon className="h-2.5 w-2.5 shrink-0 text-muted-foreground/50" />
        <span className={cn(
          "font-mono",
          calling ? "text-muted-foreground" : "text-muted-foreground/60"
        )}>
          {item.name}
        </span>
        <span className={cn(
          "ml-1",
          calling
            ? "text-muted-foreground/60"
            : isErr
              ? "text-destructive/60"
              : "text-muted-foreground/40"
        )}>
          {calling ? "调用中" : isErr ? "调用失败" : "调用完成"}
        </span>
        {hasDetail && (
          <ChevronRightIcon className={cn(
            "h-3 w-3 ml-auto shrink-0 text-muted-foreground/40 transition-transform",
            expanded && "rotate-90"
          )} />
        )}
      </CollapsibleTrigger>

      {hasDetail && (
        <CollapsibleContent>
          <div className={cn(
            "ml-5 mt-1 px-3 py-2 rounded-md text-xs space-y-2",
            "bg-muted/40 text-muted-foreground/70 border border-border/30"
          )}>
            {item.args && (
              <div className="space-y-1">
                <div className="text-[11px] uppercase tracking-wide text-muted-foreground/50">参数</div>
                <pre className="overflow-auto max-h-32 whitespace-pre-wrap break-all">{item.args}</pre>
              </div>
            )}
            {item.output && (
              <div className="space-y-1">
                <div className="text-[11px] uppercase tracking-wide text-muted-foreground/50">结果</div>
                <pre className="overflow-auto max-h-32 whitespace-pre-wrap break-all">{item.output}</pre>
              </div>
            )}
            {item.error && (
              <div className="space-y-1">
                <div className="text-[11px] uppercase tracking-wide text-destructive/70">错误</div>
                <pre className="overflow-auto max-h-32 whitespace-pre-wrap break-all text-destructive/80">{item.error}</pre>
              </div>
            )}
          </div>
        </CollapsibleContent>
      )}
    </Collapsible>
  );
}

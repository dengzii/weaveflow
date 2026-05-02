import { useEffect, useState } from "react";
import { Brain, ChevronDown, ChevronRight } from "lucide-react";
import { cn, renderMd } from "../lib/utils";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./ui/collapsible";

interface Props {
  text: string;
  done: boolean;
}

export function ThinkingBlock({ text, done }: Props) {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (done) {
      setOpen(false);
    }
  }, [done]);

  if (!done) {
    return (
      <div className="flex justify-start">
        <div className="max-w-[85%] rounded-xl border border-border/50 bg-muted/50 px-4 py-2.5 text-xs text-muted-foreground flex items-start gap-2">
          <Brain className="mt-0.5 h-3.5 w-3.5 shrink-0 animate-pulse" />
          <div
            className="min-w-0 flex-1 break-words italic prose prose-sm max-w-none [&_p]:my-0"
            dangerouslySetInnerHTML={{ __html: renderMd(text || "思考中...") }}
          />
        </div>
      </div>
    );
  }

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <div className="flex justify-start">
        <div
          className={cn(
            "max-w-[85%] overflow-hidden rounded-xl border border-border/50 bg-muted/30 text-sm",
            open && "w-full max-w-[85%]",
          )}
        >
          <CollapsibleTrigger className="flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-muted/50">
            <Brain className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <span className="flex-1 text-xs font-medium text-muted-foreground">
              {open ? "收起思考过程" : "查看思考过程"}
            </span>
            {open ? (
              <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            )}
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div
              className="max-w-none border-t border-border/50 px-4 py-3 text-xs text-muted-foreground prose prose-sm [&_p]:my-0"
              dangerouslySetInnerHTML={{ __html: renderMd(text) }}
            />
          </CollapsibleContent>
        </div>
      </div>
    </Collapsible>
  );
}

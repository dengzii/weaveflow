import { useState } from "react";
import { ChevronDown, ChevronRight, Brain } from "lucide-react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./ui/collapsible";
import { cn } from "../lib/utils";

interface Props {
  text: string;
  done: boolean;
  renderMd: (src: string) => string;
}

export function ThinkingBlock({ text, done, renderMd }: Props) {
  const [open, setOpen] = useState(false);

  if (!done) {
    return (
      <div className="flex justify-start">
        <div className="max-w-[85%] rounded-xl px-4 py-2.5 text-xs text-muted-foreground bg-muted/50 border border-border/50 flex items-start gap-2">
          <Brain className="h-3.5 w-3.5 mt-0.5 shrink-0 animate-pulse" />
          <span className="italic line-clamp-2 prose prose-sm"
            dangerouslySetInnerHTML={{ __html: renderMd(text || "思考中…") }}
          />
        </div>
      </div>
    );
  }

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <div className="flex justify-start">
        <div className={cn(
          "max-w-[85%] rounded-xl border border-border/50 bg-muted/30 overflow-hidden text-sm",
          open && "w-full max-w-[85%]"
        )}>
          <CollapsibleTrigger className="flex items-center gap-2 px-3 py-2 w-full text-left hover:bg-muted/50 transition-colors cursor-pointer">
            <Brain className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
            <span className="text-xs text-muted-foreground font-medium flex-1">
              {open ? "收起思考过程" : "查看思考过程"}
            </span>
            {open
              ? <ChevronDown className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
              : <ChevronRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
            }
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div
              className="px-4 py-3 border-t border-border/50 text-xs text-muted-foreground prose prose-sm max-w-none"
              dangerouslySetInnerHTML={{ __html: renderMd(text) }}
            />
          </CollapsibleContent>
        </div>
      </div>
    </Collapsible>
  );
}

import { useEffect, useRef, useState } from "react";
import { ArrowDownIcon, Bot } from "lucide-react";
import type { MessageItem } from "../types";
import { renderMd, cn } from "../lib/utils";
import { ThinkingBlock } from "./ThinkingBlock";
import { StepRow } from "./StepRow";
import { ToolRow } from "./ToolRow";

const NEAR_BOTTOM = 120;

interface Props {
  messages: MessageItem[];
  running: boolean;
}

export function MessageList({ messages, running }: Props) {
  const bottomRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const [showScrollBtn, setShowScrollBtn] = useState(false);
  const initialScrolled = useRef(false);

  function atBottom() {
    const el = containerRef.current;
    if (!el) return true;
    return el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM;
  }

  function scrollToBottom(behavior: ScrollBehavior = "smooth") {
    bottomRef.current?.scrollIntoView({ behavior });
  }

  // Scroll to bottom once after history is loaded (messages go from empty to populated)
  useEffect(() => {
    if (initialScrolled.current) return;
    if (messages.length > 0) {
      scrollToBottom("instant");
      initialScrolled.current = true;
    }
  }, [messages]);

  // Auto-scroll during streaming when already near bottom
  useEffect(() => {
    if (!running) return;
    if (atBottom()) {
      scrollToBottom("instant");
      setShowScrollBtn(false);
    }
  }, [messages, running]);

  // Show/hide scroll button based on scroll position
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const onScroll = () => {
      if (el.scrollHeight <= el.clientHeight) { setShowScrollBtn(false); return; }
      setShowScrollBtn(!atBottom());
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, []);

  return (
    <div className="flex-1 relative min-h-0">
      {messages.length === 0 && (
        <div className="absolute inset-x-0 bottom-1/4 flex flex-col items-center gap-2 text-muted-foreground pointer-events-none">
          <Bot className="h-8 w-8 opacity-20" />
          <p className="text-sm">有什么我可以帮你的？</p>
        </div>
      )}

      <div ref={containerRef} className="h-full overflow-y-auto py-4">
        <div className="chat-content px-6 space-y-1.5">
          {messages.map((m) => {
            switch (m.kind) {
              case "user":
                return (
                  <div key={m.id} className="flex justify-end mb-3">
                    <div className={cn(
                      "max-w-[75%] rounded-2xl rounded-tr-sm px-4 py-2.5 text-sm",
                      "bg-primary text-primary-foreground"
                    )}>
                      {m.text}
                    </div>
                  </div>
                );

              case "step":
                return <StepRow key={m.id} item={m} />;

              case "thinking":
                return (
                  <div key={m.id} className="my-2">
                    <ThinkingBlock text={m.text} done={m.done} />
                  </div>
                );

              case "tool":
                return <ToolRow key={m.id} item={m} />;

              case "assistant":
                return (
                  <div key={m.id} className="flex justify-start mt-2 mb-1">
                    <div
                      className="max-w-[85%] rounded-2xl rounded-tl-sm px-4 py-2.5 text-sm bg-card border border-border prose"
                      dangerouslySetInnerHTML={{ __html: renderMd(m.text) }}
                    />
                  </div>
                );

              case "error":
                return (
                  <div key={m.id} className="flex justify-start my-1">
                    <div className="max-w-[85%] rounded-lg px-4 py-2.5 text-sm bg-destructive/10 text-destructive border border-destructive/20">
                      {m.text}
                    </div>
                  </div>
                );

              case "stopped":
                return (
                  <div key={m.id} className="flex justify-center my-2">
                    <span className="text-xs text-muted-foreground bg-muted px-3 py-1 rounded-full">
                      已停止
                    </span>
                  </div>
                );

              default:
                return null;
            }
          })}
          <div ref={bottomRef} />
        </div>
      </div>

      {showScrollBtn && (
        <div className="absolute bottom-4 inset-x-0 flex justify-center pointer-events-none z-10">
          <button
            onClick={() => { scrollToBottom(); setShowScrollBtn(false); }}
            className={cn(
              "pointer-events-auto",
              "flex items-center justify-center",
              "h-8 w-8 rounded-full shadow-md",
              "bg-background border border-border",
              "text-muted-foreground hover:text-foreground hover:border-foreground/30",
              "transition-colors"
            )}
            aria-label="滚动到底部"
          >
            <ArrowDownIcon className="h-4 w-4" />
          </button>
        </div>
      )}
    </div>
  );
}

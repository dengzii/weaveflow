import { useEffect, useRef } from "react";
import type { MessageItem } from "../types";
import { ThinkingBlock } from "./ThinkingBlock";
import { TimelineRow } from "./TimelineRow";
import { cn } from "../lib/utils";

interface Props {
  messages: MessageItem[];
  renderMd: (src: string) => string;
}

export function MessageList({ messages, renderMd }: Props) {
  const bottomRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const { scrollTop, scrollHeight, clientHeight } = el;
    if (scrollHeight - scrollTop - clientHeight < 80) {
      bottomRef.current?.scrollIntoView({ behavior: "instant" });
    }
  }, [messages]);

  return (
    <div
      ref={containerRef}
      className="flex-1 overflow-y-auto py-4"
    >
      <div className="chat-content px-6 space-y-3">
      {messages.map((m) => {
        switch (m.kind) {
          case "user":
            return (
              <div key={m.id} className="flex justify-end">
                <div className={cn(
                  "max-w-[75%] rounded-2xl rounded-tr-sm px-4 py-2.5 text-sm",
                  "bg-primary text-primary-foreground"
                )}>
                  {m.text}
                </div>
              </div>
            );

          case "thinking":
            return <ThinkingBlock key={m.id} text={m.text} done={m.done} renderMd={renderMd} />;

          case "timeline":
            return <TimelineRow key={m.id} entries={m.entries} />;

          case "assistant":
            return (
              <div key={m.id} className="flex justify-start">
                <div
                  className="max-w-[85%] rounded-2xl rounded-tl-sm px-4 py-2.5 text-sm bg-card border border-border prose"
                  dangerouslySetInnerHTML={{ __html: renderMd(m.text) }}
                />
              </div>
            );

          case "error":
            return (
              <div key={m.id} className="flex justify-start">
                <div className="max-w-[85%] rounded-lg px-4 py-2.5 text-sm bg-destructive/10 text-destructive border border-destructive/20">
                  {m.text}
                </div>
              </div>
            );

          case "stopped":
            return (
              <div key={m.id} className="flex justify-center">
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
  );
}

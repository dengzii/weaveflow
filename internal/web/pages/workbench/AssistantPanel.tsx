import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Loader2, Send, Sparkles, X } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  ApiError,
  getAssistantSession,
  getAssistantStatus,
  streamAssistantJob,
  submitAssistantMessage,
} from "../../api";
import type { AssistantActivity, AssistantMessage, GraphDefinition } from "../../types";
import { Button } from "../../components/ui/button";
import { cn } from "../../lib/utils";

interface AssistantPanelProps {
  graphID: string;
  graphVersion: string;
  definition: GraphDefinition | null;
  selectedRunID: string;
  workspaceMode: string;
  onGraphRefresh: () => Promise<void>;
}

export function AssistantPanel({
  graphID,
  graphVersion,
  definition,
  selectedRunID,
  workspaceMode,
  onGraphRefresh,
}: AssistantPanelProps) {
  const [enabled, setEnabled] = useState(false);
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [messages, setMessages] = useState<AssistantMessage[]>([]);
  const [activities, setActivities] = useState<AssistantActivity[]>([]);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [triggerSlot, setTriggerSlot] = useState<HTMLElement | null>(null);
  const sessionID = useMemo(() => assistantSessionID(graphID), [graphID]);
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setTriggerSlot(document.getElementById("workbench-assistant-trigger-slot"));
  }, []);

  useEffect(() => {
    let active = true;
    void getAssistantStatus().then((status) => {
      if (active) setEnabled(status.enabled);
    }).catch(() => {
      if (active) setEnabled(false);
    });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    setMessages([]);
    setActivities([]);
    setError("");
  }, [sessionID]);

  useEffect(() => {
    if (!open || messages.length > 0) return;
    let active = true;
    void getAssistantSession(sessionID).then((session) => {
      if (active) setMessages(session.messages ?? []);
    }).catch(() => undefined);
    return () => { active = false; };
  }, [open, messages.length, sessionID]);

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [activities, messages, pending]);

  if (!enabled) return null;

  async function send() {
    const text = message.trim();
    if (!text || pending) return;
    setMessage("");
    setError("");
    setActivities([]);
    setPending(true);
    setMessages((current) => [...current, { role: "user", content: text, created_at: new Date().toISOString() }]);
    try {
      const job = await submitAssistantMessage(sessionID, text, {
        graph_id: graphID,
        graph_version: graphVersion,
        definition,
        selected_run_id: selectedRunID || undefined,
        workspace_mode: workspaceMode,
      });
      setActivities(job.activities ?? []);
      const current = await streamAssistantJob(job.job_id, (update) => {
        setActivities(update.activities ?? []);
      });
      if (current.status === "completed") {
        setMessages((items) => [...items, {
          role: "assistant",
          content: current.reply ?? "",
          created_at: current.updated_at,
        }]);
        if (current.mutated) {
          try {
            await onGraphRefresh();
          } catch (cause) {
            setError(`Graph updated, but the Workbench refresh failed: ${cause instanceof Error ? cause.message : String(cause)}`);
          }
        }
      } else {
        setError(current.error || "Assistant request failed");
      }
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : String(cause));
    } finally {
      setPending(false);
    }
  }

  return (
    <>
      {triggerSlot ? createPortal(
        <button
          type="button"
          aria-label={open ? "Close Assistant" : "Open Assistant"}
          title={open ? "Close Assistant" : "Assistant"}
          onClick={() => setOpen((value) => !value)}
          className="flex h-9 w-9 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-lg transition hover:scale-105 hover:shadow-xl"
        >
          <span className={cn("transition-transform duration-200", open ? "rotate-90" : "rotate-0")}>
            {open ? <X className="h-4 w-4" /> : <Sparkles className="h-4 w-4" />}
          </span>
        </button>,
        triggerSlot
      ) : null}
      <section
        aria-hidden={!open}
        className={cn(
          "assistant-panel fixed bottom-20 right-6 z-[300] flex h-[min(620px,calc(100vh-7rem))] w-[min(420px,calc(100vw-2rem))] origin-bottom-right transform-gpu flex-col overflow-hidden rounded-2xl border border-border bg-panel shadow-2xl transition-[opacity,transform] duration-200 ease-out",
          open ? "pointer-events-auto translate-y-0 scale-100 opacity-100" : "pointer-events-none translate-y-2 scale-[0.98] opacity-0"
        )}
      >
          <header className="flex items-center gap-2.5 border-b border-border px-3.5 py-2.5">
            <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-primary/10 text-primary"><Sparkles className="h-3.5 w-3.5" /></div>
            <div className="min-w-0 flex-1">
              <h2 className="text-[13px] font-semibold tracking-tight">Assistant</h2>
            </div>
            <button type="button" aria-label="Close assistant" className="rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground" onClick={() => setOpen(false)}><X className="h-4 w-4" /></button>
          </header>
          <div className="min-h-0 flex-1 space-y-2.5 overflow-y-auto bg-background/30 p-3 text-[13px] leading-5">
            {messages.length === 0 ? <p className="rounded-2xl border border-border/70 bg-muted/45 px-3 py-2.5 text-muted-foreground shadow-sm">告诉我你想怎样调整这个 Graph，或让我先检查当前定义。</p> : null}
            {messages.map((item, index) => (
              <div key={`${item.created_at}-${index}`} className={cn("flex w-full", item.role === "user" ? "justify-end" : "justify-start")}>
                <div className={cn("min-w-0 max-w-[90%] overflow-x-auto rounded-2xl px-3 py-2.5 shadow-sm", item.role === "user" ? "rounded-br-md bg-primary text-primary-foreground" : "rounded-bl-md border border-border/70 bg-panel text-foreground")}>
                  {item.role === "assistant" ? <div className="assistant-markdown"><ReactMarkdown remarkPlugins={[remarkGfm]}>{item.content}</ReactMarkdown></div> : <p className="whitespace-pre-wrap break-words">{item.content}</p>}
                </div>
              </div>
            ))}
            {pending ? (
              <div className="flex w-fit max-w-[90%] items-start gap-2 rounded-2xl rounded-bl-md border border-border/70 bg-panel px-3 py-2.5 text-muted-foreground shadow-sm">
                <Loader2 className="mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin" />
                {activities.length === 0 ? (
                  <span>正在分析当前 Graph…</span>
                ) : (
                  <div className="space-y-1.5">
                    {activities.map((activity) => (
                      <p key={`${activity.round}-${activity.created_at}`} className="whitespace-pre-wrap break-words text-foreground/80">
                        {activity.content}
                      </p>
                    ))}
                  </div>
                )}
              </div>
            ) : null}
            {error ? <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-3 py-2 text-[12px] text-destructive">{error}</div> : null}
            <div ref={endRef} />
          </div>
          <form className="border-t border-border p-3" onSubmit={(event) => { event.preventDefault(); void send(); }}>
            <div className="flex items-end gap-2 rounded-xl border border-input bg-background p-2 focus-within:ring-2 focus-within:ring-ring">
              <textarea value={message} onChange={(event) => setMessage(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(); } }} placeholder="描述你的 Graph 需求…" rows={2} disabled={pending} className="min-h-10 flex-1 resize-none border-0 bg-transparent px-1 py-1 text-[13px] leading-5 outline-none placeholder:text-muted-foreground" />
              <Button type="submit" size="sm" disabled={pending || !message.trim()} aria-label="Send assistant message"><Send className="h-3.5 w-3.5" /></Button>
            </div>
            <p className="mt-1.5 px-1 text-[10px] text-muted-foreground">Enter 发送，Shift+Enter 换行；对话仅保留最近几轮。</p>
          </form>
      </section>
    </>
  );
}

function assistantSessionID(graphID: string): string {
  const key = `weaveflow.assistant.session.v1.${graphID || "draft"}`;
  try {
    const existing = window.localStorage.getItem(key);
    if (existing) return existing;
    const generated = typeof crypto.randomUUID === "function" ? crypto.randomUUID() : `session-${Date.now()}`;
    window.localStorage.setItem(key, generated);
    return generated;
  } catch {
    return `session-${Date.now()}`;
  }
}

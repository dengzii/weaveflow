import { useEffect, useMemo, useRef, useState } from "react";
import { Bot, Brain, CheckCircle2, CircleAlert, Loader2, MessageCircle, Plus, Route, Send, Sparkles, Wrench, X } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { getRunInspection, listEvents, normalizeTriggerToken, streamChatTrigger } from "../../api";
import { Button } from "../../components/ui/button";
import { Input, SensitiveInput } from "../../components/ui/input";
import { Textarea } from "../../components/ui/textarea";
import { getTriggerToken, setStoredTriggerToken } from "../../lib/backend";
import type {
  ChatReply,
  ChatResult,
  GraphNodeSpec,
  RunRecord,
  RunStatus,
  RuntimeEvent,
  Trigger,
} from "../../types";
import { StatusText, type StatusTone } from "./shared";
import { buildChatExecutionSteps, chatNodePresentation, type ChatExecutionStep, type ChatNodeKind } from "./chatPresentation";

interface ChatPanelProps {
  graphID: string;
  triggers: Trigger[];
  runtimeEvents: RuntimeEvent[];
  nodes?: GraphNodeSpec[];
  onClose: () => void;
  onBusyChange?: (busy: boolean) => void;
}

interface ChatTurn {
  id: string;
  createdAt: number;
  userContent: string;
  replies: ChatReply[];
  events: RuntimeEvent[];
  runID?: string;
  run?: RunRecord;
  error: string;
  status: "connecting" | "running" | RunStatus;
}

const chatPanelClassName = "flex h-full min-h-0 min-w-0 w-[min(360px,calc(100vw-4rem))] max-w-full shrink-0 flex-col overflow-hidden border-r border-border bg-panel";

export function isHTTPChatTrigger(trigger: Trigger): boolean {
  if (trigger.type !== "chat" || !trigger.enabled) return false;
  return (trigger.chat?.channel?.trim().toLowerCase() || "http") === "http";
}

export function ChatPanel({
  graphID,
  triggers,
  runtimeEvents,
  nodes = [],
  onClose,
  onBusyChange,
}: ChatPanelProps) {
  const chatTriggers = useMemo(() => triggers.filter(isHTTPChatTrigger), [triggers]);
  const [selectedTriggerID, setSelectedTriggerID] = useState("");
  const [conversationID, setConversationID] = useState(() => newConversationID());
  const [userID, setUserID] = useState("webui-user");
  const [message, setMessage] = useState("");
  const [triggerToken, setTriggerToken] = useState(getTriggerToken);
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const abortRef = useRef<AbortController | null>(null);
  const endRef = useRef<HTMLDivElement>(null);

  const selectedTrigger = useMemo(
    () => chatTriggers.find((trigger) => trigger.id === selectedTriggerID) ?? chatTriggers[0],
    [chatTriggers, selectedTriggerID]
  );
  const selectedTriggerLabel = selectedTrigger?.name?.trim() || selectedTrigger?.id || "Chat Trigger";
  const triggerCredentialConfigured = Boolean(selectedTrigger?.credential_configured || selectedTrigger?.credential?.ref?.trim());
  const triggerTokenConfigured = Boolean(triggerToken.trim());

  useEffect(() => {
    if (!selectedTrigger) {
      setSelectedTriggerID("");
      return;
    }
    if (selectedTriggerID !== selectedTrigger.id) {
      setSelectedTriggerID(selectedTrigger.id);
      setConversationID(newConversationID());
      setTurns([]);
      setError("");
    }
  }, [selectedTrigger, selectedTriggerID]);

  useEffect(() => {
    onBusyChange?.(pending);
  }, [onBusyChange, pending]);

  useEffect(() => () => {
    abortRef.current?.abort();
    onBusyChange?.(false);
  }, [onBusyChange]);

  useEffect(() => {
    if (runtimeEvents.length === 0) return;
    setTurns((current) => {
      const activeTurn = current.find((turn) => turn.status === "connecting" || turn.status === "running");
      if (!activeTurn) return current;
      const candidateEvents = runtimeEvents.filter((event) => {
        if (event.graph_id && event.graph_id !== graphID) return false;
        if (event.run_id === activeTurn.runID) return true;
        if (activeTurn.runID || Date.parse(event.timestamp) < activeTurn.createdAt - 2_000) return false;
        return event.type === "run.created" || event.type === "run.started";
      });
      const runID = activeTurn.run?.run_id || candidateEvents[0]?.run_id;
      const matchingEvents = runtimeEvents.filter((event) => event.run_id === runID);
      if (!runID && matchingEvents.length === 0) return current;
      return current.map((turn) => {
        if (turn.id !== activeTurn.id) return turn;
        const known = new Set(turn.events.map((event) => event.id));
        const nextEvents = [...turn.events, ...matchingEvents.filter((event) => !known.has(event.id))].slice(-300);
        return {
          ...turn,
          runID: turn.runID ?? runID,
          events: nextEvents,
          status: "running" as const,
        };
      });
    });
  }, [graphID, runtimeEvents]);

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [turns, pending]);

  function updateTurn(turnID: string, update: (turn: ChatTurn) => ChatTurn) {
    setTurns((current) => current.map((turn) => turn.id === turnID ? update(turn) : turn));
  }

  async function send() {
    const content = message.trim();
    const normalizedUserID = userID.trim();
    if (!content || pending || !selectedTrigger) return;
    if (!normalizedUserID) {
      setError("User ID is required");
      return;
    }
    if (!triggerCredentialConfigured) {
      setError("This Chat Trigger has no credential configured. Add a Trigger credential before sending chat messages.");
      return;
    }
    let normalizedToken: string;
    try {
      normalizedToken = normalizeTriggerToken(triggerToken);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      return;
    }
    if (!normalizedToken) {
      setError("Enter the Chat Trigger token before sending messages.");
      return;
    }
    const turnID = newTurnID();
    const turn: ChatTurn = {
      id: turnID,
      createdAt: Date.now(),
      userContent: content,
      replies: [],
      events: [],
      error: "",
      status: "connecting",
    };
    setTurns((current) => [...current, turn]);
    setMessage("");
    setError("");
    setPending(true);
    const controller = new AbortController();
    abortRef.current = controller;
    try {
      const outcome = await streamChatTrigger(
        graphID,
        selectedTrigger.id,
        {
          message_id: newTurnID(),
          user_id: normalizedUserID,
          conversation_id: conversationID,
          content,
        },
        normalizedToken,
        {
          onReply: (reply) => updateTurn(turnID, (current) => ({
            ...current,
            replies: [...current.replies, reply],
            status: "running",
          })),
          onResult: (result) => updateTurnWithResult(turnID, result),
          onError: (message, result) => updateTurn(turnID, (current) => ({
            ...current,
            ...(result ? { runID: result.run.run_id, run: result.run } : {}),
            error: message,
            status: result ? normalizedRunStatus(result.run.status) : "failed",
          })),
        },
        controller.signal
      );
      if (outcome.result) {
        updateTurnWithResult(turnID, outcome.result);
        try {
          const page = await listEvents(graphID, outcome.result.run.run_id, undefined, 500);
          updateTurn(turnID, (current) => ({
            ...current,
            events: mergeEvents(current.events, page.items),
          }));
        } catch {
          try {
            const inspection = await getRunInspection(graphID, outcome.result.run.run_id);
            updateTurn(turnID, (current) => ({
              ...current,
              events: mergeEvents(current.events, inspection.events.items),
            }));
          } catch {
            setError("Chat finished, but runtime events could not be loaded");
          }
        }
      }
      if (outcome.error) setError(outcome.error);
    } catch (cause) {
      if (!controller.signal.aborted) {
        const messageText = cause instanceof Error ? cause.message : String(cause);
        updateTurn(turnID, (current) => ({ ...current, error: messageText, status: "failed" }));
        setError(messageText);
      }
    } finally {
      if (abortRef.current === controller) abortRef.current = null;
      setPending(false);
    }
  }

  function updateTurnWithResult(turnID: string, result: ChatResult) {
    updateTurn(turnID, (current) => ({
      ...current,
      runID: result.run.run_id,
      run: result.run,
      status: normalizedRunStatus(result.run.status),
    }));
  }

  function startNewConversation() {
    if (pending) return;
    setConversationID(newConversationID());
    setTurns([]);
    setError("");
  }

  if (!selectedTrigger) {
    return (
      <aside aria-label="Chat panel" className={chatPanelClassName}>
        <ChatPanelHeader subtitle="No trigger" onClose={onClose} />
        <div className="p-4 text-sm text-muted-foreground">No Chat Trigger available.</div>
      </aside>
    );
  }

  return (
    <aside aria-label="Chat panel" className={chatPanelClassName}>
      <ChatPanelHeader subtitle={selectedTriggerLabel} onClose={onClose} />
      <div className="grid min-w-0 shrink-0 gap-2.5 border-b border-border p-3">
        {chatTriggers.length > 1 ? (
          <label className="grid gap-1 text-xs font-medium">
            Trigger
            <select
              value={selectedTrigger.id}
              onChange={(event) => setSelectedTriggerID(event.target.value)}
              disabled={pending}
              className="h-8 min-w-0 rounded-md border border-input bg-background px-2 text-xs outline-none focus:border-ring"
            >
              {chatTriggers.map((trigger) => <option key={trigger.id} value={trigger.id}>{trigger.name || trigger.id}</option>)}
            </select>
          </label>
        ) : null}
        <label className="grid gap-1 text-xs font-medium">
          User ID
          <Input value={userID} onChange={(event) => setUserID(event.target.value)} disabled={pending} className="h-8 text-xs" />
        </label>
        <label className="grid gap-1 text-xs font-medium">
          Trigger token
          <SensitiveInput
            value={triggerToken}
            onValueChange={(value) => {
              setTriggerToken(value);
              setError("");
            }}
            onBlur={() => {
              try {
                const normalized = normalizeTriggerToken(triggerToken);
                setTriggerToken(normalized);
                void setStoredTriggerToken(normalized);
              } catch (cause) {
                setError(cause instanceof Error ? cause.message : String(cause));
              }
            }}
            placeholder="Enter the token configured on this Trigger"
            disabled={pending}
            className="h-8 text-xs"
          />
        </label>
        <div className="flex min-w-0 items-center gap-2 rounded-md border border-border bg-muted/25 px-2.5 py-2">
          <div className="min-w-0 flex-1">
            <div className="text-[10px] font-medium text-muted-foreground">Conversation ID</div>
            <div className="truncate font-mono text-[10px] text-foreground/80" title={conversationID}>{conversationID}</div>
          </div>
          <Button type="button" variant="outline" size="sm" className="h-7 shrink-0 px-2 text-[11px]" aria-label="New conversation" title="New conversation" onClick={startNewConversation} disabled={pending}>
            <Plus className="h-3 w-3" /> New
          </Button>
        </div>
        {!triggerCredentialConfigured ? (
          <p className="text-[10px] text-destructive">Trigger credential 未配置。</p>
        ) : !triggerTokenConfigured ? (
          <p className="text-[10px] text-destructive">请输入 Trigger token。</p>
        ) : null}
      </div>

      <div className="min-h-0 min-w-0 flex-1 space-y-3 overflow-x-hidden overflow-y-auto bg-background/30 p-3">
        {turns.length === 0 ? (
          <div className="rounded-lg border border-dashed border-border px-3 py-4 text-center text-xs text-muted-foreground">
            发送消息开始对话
          </div>
        ) : null}
        {turns.map((turn) => <ChatTurnView key={turn.id} turn={turn} nodes={nodes} />)}
        {error ? <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{error}</div> : null}
        <div ref={endRef} />
      </div>

      <form
        className="min-w-0 shrink-0 border-t border-border p-3"
        onSubmit={(event) => { event.preventDefault(); void send(); }}
      >
        <div className="flex min-w-0 items-end gap-2 rounded-lg border border-input bg-background p-2 focus-within:ring-2 focus-within:ring-ring">
          <Textarea
            value={message}
            onChange={(event) => setMessage(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                void send();
              }
            }}
            placeholder="发送消息…"
            rows={2}
            disabled={pending}
            className="min-h-12 min-w-0 flex-1 overflow-y-auto border-0 bg-transparent px-1 py-1 font-sans text-xs shadow-none focus:border-0"
          />
          <Button type="submit" size="sm" className="h-8 w-8 shrink-0 px-0" aria-label="Send message" title="Send" disabled={pending || !message.trim()}>
            {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3.5 w-3.5" />}
          </Button>
        </div>
        <p className="mt-1.5 px-1 text-[10px] text-muted-foreground">Enter 发送 · Shift+Enter 换行</p>
      </form>
    </aside>
  );
}

function ChatPanelHeader({ subtitle, onClose }: { subtitle: string; onClose: () => void }) {
  return (
    <header className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-3">
      <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-primary/10 text-primary"><MessageCircle className="h-4 w-4" /></div>
      <div className="min-w-0 flex-1">
        <h2 className="text-sm font-semibold">Chat</h2>
        <p className="truncate text-[10px] text-muted-foreground" title={subtitle}>{subtitle}</p>
      </div>
      <button type="button" aria-label="Close Chat" title="Close Chat" onClick={onClose} className="rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground">
        <X className="h-4 w-4" />
      </button>
    </header>
  );
}

function ChatTurnView({ turn, nodes }: { turn: ChatTurn; nodes: GraphNodeSpec[] }) {
  const updates = [...turn.replies].reverse().find((reply) => reply.kind === "update" && reply.content?.trim());
  const finish = [...turn.replies].reverse().find((reply) => reply.kind === "finish" && reply.content?.trim());
  const statusTone = chatStatusTone(turn.status);
  const activeNode = [...turn.events].reverse().find((event) => event.node_id)?.node_id;
  const activePresentation = chatNodePresentation(activeNode, nodes);

  return (
    <article className="min-w-0 space-y-2">
      <div className="flex justify-end">
        <div className="max-w-[88%] rounded-lg rounded-br-sm bg-primary px-3 py-2 text-xs text-primary-foreground shadow-sm whitespace-pre-wrap break-words">{turn.userContent}</div>
      </div>
      <div className="min-w-0 max-w-[96%] space-y-3 overflow-hidden rounded-xl rounded-bl-sm border border-border/70 bg-panel px-3 py-3 shadow-sm">
        <div className="flex min-w-0 items-center gap-2 text-[10px]">
          <StatusText tone={statusTone}>{turn.status}</StatusText>
          {activeNode ? <span className="min-w-0 flex-1 truncate text-muted-foreground">{activePresentation.label}</span> : null}
          {turn.run?.run_id ? <span className="ml-auto max-w-[45%] truncate font-mono text-muted-foreground" title={turn.run.run_id}>{turn.run.run_id}</span> : null}
        </div>
        <ChatStepList events={turn.events} replies={turn.replies} nodes={nodes} progress={updates?.content} />
        {finish ? (
          <div className="rounded-lg border border-emerald-500/25 bg-emerald-500/5 px-3 py-2.5">
            <div className="mb-1.5 flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wide text-emerald-700 dark:text-emerald-300"><CheckCircle2 className="h-3 w-3" /> Final response</div>
            <div className="assistant-markdown overflow-x-auto text-xs"><ReactMarkdown remarkPlugins={[remarkGfm]}>{finish.content ?? ""}</ReactMarkdown></div>
          </div>
        ) : null}
        {turn.status === "connecting" || turn.status === "running" ? (
          <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" /> Graph is running…</div>
        ) : null}
        {turn.error ? <div className="rounded border border-destructive/30 bg-destructive/10 px-2 py-1 text-[10px] text-destructive">{turn.error}</div> : null}
      </div>
    </article>
  );
}

function ChatStepList({ events, replies, nodes, progress }: { events: RuntimeEvent[]; replies: ChatReply[]; nodes: GraphNodeSpec[]; progress?: string }) {
  const steps = buildChatExecutionSteps(events, replies, nodes);
  if (steps.length === 0 && !progress) return null;
  return (
    <div className="space-y-0.5">
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Steps</div>
      {steps.map((step, index) => <ChatStep key={step.id} step={step} index={index} last={index === steps.length - 1} />)}
      {progress && steps.length === 0 ? <div className="pl-6 text-xs text-muted-foreground">{progress}</div> : null}
    </div>
  );
}

function ChatStep({ step, index, last }: { step: ChatExecutionStep; index: number; last: boolean }) {
  const Icon = nodeKindIcon(step.node.kind);
  const active = step.status === "running" || step.status === "retrying";
  return (
    <section className="relative grid grid-cols-[18px_minmax(0,1fr)] gap-2 pb-2.5">
      {!last ? <span className="absolute bottom-0 left-[8px] top-4 w-px bg-border" /> : null}
      <span className={`relative z-10 flex h-[18px] w-[18px] items-center justify-center rounded-full border ${step.status === "failed" ? "border-destructive/50 bg-destructive/10 text-destructive" : active ? "border-primary/50 bg-primary/10 text-primary" : "border-emerald-500/40 bg-emerald-500/10 text-emerald-600"}`}>
        {active ? <Loader2 className="h-3 w-3 animate-spin" /> : step.status === "failed" ? <CircleAlert className="h-3 w-3" /> : <CheckCircle2 className="h-3 w-3" />}
      </span>
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-1.5">
          <Icon className="h-3 w-3 shrink-0 text-muted-foreground" />
          <span className="truncate text-[11px] font-semibold">{index + 1}. {step.node.label}</span>
          <span className="shrink-0 text-[9px] text-muted-foreground">{step.node.type}</span>
        </div>
        <div className={`mt-0.5 text-[11px] ${active ? "text-foreground" : "text-muted-foreground"}`}>{step.action}</div>
        {step.result ? <div className={`mt-1 rounded-md px-2 py-1.5 text-[10px] ${step.status === "failed" ? "bg-destructive/10 text-destructive" : "bg-muted/40 text-muted-foreground"}`}>{step.result}</div> : null}
        {step.replies.length > 0 ? (
          <div className="mt-1.5 space-y-2 rounded-lg border border-border/70 bg-background/50 px-3 py-2.5">
            {step.replies.map((reply, replyIndex) => <div key={reply.sequence ?? replyIndex} className="assistant-markdown overflow-x-auto text-xs leading-relaxed"><ReactMarkdown remarkPlugins={[remarkGfm]}>{reply.content ?? ""}</ReactMarkdown></div>)}
          </div>
        ) : null}
      </div>
    </section>
  );
}

function nodeKindIcon(kind: ChatNodeKind) {
  if (kind === "agent") return Brain;
  if (kind === "model") return Sparkles;
  if (kind === "tool") return Wrench;
  if (kind === "reply") return Bot;
  if (kind === "control") return Route;
  return CircleAlert;
}

function mergeEvents(current: RuntimeEvent[], incoming: RuntimeEvent[]): RuntimeEvent[] {
  const seen = new Set(current.map((event) => event.id));
  return [...current, ...incoming.filter((event) => !seen.has(event.id))].slice(-300);
}

function chatStatusTone(status: ChatTurn["status"]): StatusTone {
  if (status === "completed") return "ok";
  if (status === "failed") return "danger";
  if (status === "paused") return "warn";
  if (status === "canceled") return "warn";
  if (status === "connecting" || status === "running") return "live";
  return "neutral";
}

function normalizedRunStatus(status: RunStatus): RunStatus {
  switch (status) {
    case "pending":
    case "running":
    case "paused":
    case "failed":
    case "completed":
    case "canceled":
      return status;
    default:
      return "failed";
  }
}

function newConversationID(): string {
  return `webui-${newTurnID()}`;
}

function newTurnID(): string {
  return globalThis.crypto?.randomUUID?.() ?? `chat-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

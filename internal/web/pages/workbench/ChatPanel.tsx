import { useEffect, useMemo, useRef, useState } from "react";
import { Loader2, Send, X } from "lucide-react";
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
import { buildChatExecutionSteps, type ChatExecutionStep } from "./chatPresentation";

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

const chatPanelClassName = "flex h-full min-h-0 min-w-0 w-[min(420px,calc(100vw-4rem))] max-w-full shrink-0 flex-col overflow-hidden border-r border-border bg-panel";

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
        <ChatPanelHeader title="No trigger" onClose={onClose} />
        <div className="p-4 text-sm text-muted-foreground">No Chat Trigger available.</div>
      </aside>
    );
  }

  return (
    <aside aria-label="Chat panel" className={chatPanelClassName}>
      <ChatPanelHeader title={selectedTriggerLabel} onClose={onClose} onNew={startNewConversation} newDisabled={pending} />
      <div className="grid min-w-0 shrink-0 gap-2 border-b border-border p-3">
        {chatTriggers.length > 1 ? (
          <label className="grid grid-cols-[64px_minmax(0,1fr)] items-center gap-2 text-xs">
            <span className="text-muted-foreground">Trigger</span>
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
        <label className="grid grid-cols-[64px_minmax(0,1fr)] items-center gap-2 text-xs">
          <span className="text-muted-foreground">User</span>
          <Input value={userID} onChange={(event) => setUserID(event.target.value)} disabled={pending} className="h-8 text-xs" />
        </label>
        <label className="grid grid-cols-[64px_minmax(0,1fr)] items-center gap-2 text-xs">
          <span className="text-muted-foreground">Token</span>
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
            placeholder="Trigger token"
            disabled={pending}
            className="h-8 text-xs"
          />
        </label>
        {!triggerCredentialConfigured ? (
          <p className="pl-[72px] text-[10px] text-destructive">缺少 Trigger 凭据</p>
        ) : !triggerTokenConfigured ? (
          <p className="pl-[72px] text-[10px] text-destructive">请输入 Trigger token</p>
        ) : null}
      </div>

      <div className="min-h-0 min-w-0 flex-1 space-y-4 overflow-x-hidden overflow-y-auto bg-background p-3">
        {turns.length === 0 ? (
          <div className="py-6 text-center text-xs text-muted-foreground">暂无消息</div>
        ) : null}
        {turns.map((turn) => <ChatTurnView key={turn.id} turn={turn} nodes={nodes} />)}
        {error ? <div className="text-xs text-destructive">{error}</div> : null}
        <div ref={endRef} />
      </div>

      <form
        className="min-w-0 shrink-0 border-t border-border p-3"
        onSubmit={(event) => { event.preventDefault(); void send(); }}
      >
        <div className="flex min-w-0 items-end gap-2">
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
            className="min-h-12 min-w-0 flex-1 overflow-y-auto px-2 py-2 font-sans text-xs"
          />
          <Button type="submit" size="sm" className="h-8 w-8 shrink-0 px-0" aria-label="Send message" title="Send" disabled={pending || !message.trim()}>
            {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3.5 w-3.5" />}
          </Button>
        </div>
      </form>
    </aside>
  );
}

function ChatPanelHeader({
  title,
  onClose,
  onNew,
  newDisabled = false,
}: {
  title: string;
  onClose: () => void;
  onNew?: () => void;
  newDisabled?: boolean;
}) {
  return (
    <header className="flex h-11 shrink-0 items-center gap-2 border-b border-border px-3">
      <h2 className="min-w-0 flex-1 truncate text-sm font-semibold" title={title}>{title}</h2>
      {onNew ? <Button type="button" variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={onNew} disabled={newDisabled}>New</Button> : null}
      <button type="button" aria-label="Close Chat" title="Close Chat" onClick={onClose} className="rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground">
        <X className="h-4 w-4" />
      </button>
    </header>
  );
}

function ChatTurnView({ turn, nodes }: { turn: ChatTurn; nodes: GraphNodeSpec[] }) {
  const updates = [...turn.replies].reverse().find((reply) => reply.kind === "update" && reply.content?.trim());
  const running = turn.status === "connecting" || turn.status === "running";

  return (
    <article className="min-w-0 border-b border-border pb-4 last:border-b-0">
      <div className="mb-3 grid grid-cols-[44px_minmax(0,1fr)] gap-2 text-xs">
        <span className="font-medium text-muted-foreground">You</span>
        <div className="whitespace-pre-wrap break-words text-foreground">{turn.userContent}</div>
      </div>
      <div className="min-w-0">
        <ChatStepList events={turn.events} replies={turn.replies} nodes={nodes} />
        {running ? (
          <div className="grid grid-cols-[24px_minmax(0,1fr)] gap-2 py-1 text-[11px] text-muted-foreground">
            <Loader2 className="mt-0.5 h-3 w-3 animate-spin" />
            <span>{updates?.content?.trim() || (turn.status === "connecting" ? "Connecting" : "Running")}</span>
          </div>
        ) : null}
        {turn.error ? <div className="ml-8 mt-1 text-[11px] text-destructive">{turn.error}</div> : null}
        <div className="ml-8 mt-1 flex min-w-0 gap-2 text-[9px] text-muted-foreground">
          <span>{turn.status}</span>
          {turn.run?.run_id ? <span className="truncate font-mono" title={turn.run.run_id}>{turn.run.run_id}</span> : null}
        </div>
      </div>
    </article>
  );
}

function ChatStepList({ events, replies, nodes }: { events: RuntimeEvent[]; replies: ChatReply[]; nodes: GraphNodeSpec[] }) {
  const steps = buildChatExecutionSteps(events, replies, nodes);
  if (steps.length === 0) return null;
  return (
    <div>
      {steps.map((step, index) => <ChatStep key={step.id} step={step} index={index} />)}
    </div>
  );
}

function ChatStep({ step, index }: { step: ChatExecutionStep; index: number }) {
  const active = step.status === "running" || step.status === "retrying";
  const failed = step.status === "failed";
  return (
    <section className="grid grid-cols-[24px_minmax(0,1fr)] gap-2 border-t border-border/70 py-2 first:border-t-0 first:pt-0">
      <span className={`pt-0.5 text-right font-mono text-[10px] tabular-nums ${failed ? "text-destructive" : active ? "text-primary" : "text-muted-foreground"}`}>{index + 1}</span>
      <div className="min-w-0">
        <div className="flex min-w-0 items-baseline gap-2">
          <span className="truncate text-[11px] font-medium">{step.node.label}</span>
          <span className={`shrink-0 text-[10px] ${failed ? "text-destructive" : "text-muted-foreground"}`}>{step.action}</span>
        </div>
        {step.content ? (
          <div className="assistant-markdown mt-1 overflow-x-auto text-xs leading-5">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{step.content}</ReactMarkdown>
          </div>
        ) : null}
        {step.result ? <div className={`mt-1 text-[11px] ${failed ? "text-destructive" : "text-muted-foreground"}`}>{step.result}</div> : null}
      </div>
    </section>
  );
}

function mergeEvents(current: RuntimeEvent[], incoming: RuntimeEvent[]): RuntimeEvent[] {
  const seen = new Set(current.map((event) => event.id));
  return [...current, ...incoming.filter((event) => !seen.has(event.id))].slice(-300);
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

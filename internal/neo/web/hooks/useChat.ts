import { useReducer, useRef, useState, useCallback } from "react";
import { marked } from "marked";
import type { MessageItem, TimelineEntry, ChatEvent, HistoryMessage } from "../types";

marked.setOptions({ breaks: true, gfm: true });

function renderMd(src: string): string {
  try { return marked.parse(src) as string; }
  catch { return src.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;"); }
}

// ── reducer ──────────────────────────────────────────────────────────────────

type Action =
  | { type: "SET"; items: MessageItem[] }
  | { type: "ADD"; item: MessageItem }
  | { type: "CLOSE_THINKING"; id: string }
  | { type: "APPEND_THINKING"; id: string; chunk: string }
  | { type: "ADD_TIMELINE"; id: string }
  | { type: "ADD_PHASE"; timelineId: string; entry: Extract<TimelineEntry, { kind: "phase" }> }
  | { type: "ADD_TOOL"; timelineId: string; toolId: string; name: string }
  | { type: "RESOLVE_TOOL"; timelineId: string; toolId: string; status: "ok" | "err"; result: string }
  | { type: "APPEND_CONTENT"; id: string; chunk: string };

function reducer(state: MessageItem[], action: Action): MessageItem[] {
  switch (action.type) {
    case "SET":
      return action.items;

    case "ADD":
      return [...state, action.item];

    case "CLOSE_THINKING":
      return state.map((m) =>
        m.id === action.id && m.kind === "thinking" ? { ...m, done: true } : m
      );

    case "APPEND_THINKING":
      return state.map((m) =>
        m.id === action.id && m.kind === "thinking"
          ? { ...m, text: m.text + action.chunk }
          : m
      );

    case "ADD_TIMELINE":
      return [...state, { id: action.id, kind: "timeline", entries: [] }];

    case "ADD_PHASE":
      return state.map((m) =>
        m.id === action.timelineId && m.kind === "timeline"
          ? { ...m, entries: [...m.entries, action.entry] }
          : m
      );

    case "ADD_TOOL":
      return state.map((m) =>
        m.id === action.timelineId && m.kind === "timeline"
          ? {
              ...m,
              entries: [
                ...m.entries,
                { id: action.toolId, kind: "tool" as const, name: action.name, status: "pending" as const, result: "..." },
              ],
            }
          : m
      );

    case "RESOLVE_TOOL":
      return state.map((m) => {
        if (m.id !== action.timelineId || m.kind !== "timeline") return m;
        return {
          ...m,
          entries: m.entries.map((e) =>
            e.kind === "tool" && e.id === action.toolId
              ? { ...e, status: action.status, result: action.result }
              : e
          ),
        };
      });

    case "APPEND_CONTENT":
      return state.map((m) =>
        m.id === action.id && m.kind === "assistant"
          ? { ...m, text: m.text + action.chunk }
          : m
      );

    default:
      return state;
  }
}

// ── stream context (mutable, no re-render) ────────────────────────────────────

interface StreamCtx {
  lastNodeId: string;
  thinkingId: string | null;
  timelineId: string | null;
  contentId: string | null;
  pendingToolId: string | null;
}

function freshCtx(): StreamCtx {
  return { lastNodeId: "", thinkingId: null, timelineId: null, contentId: null, pendingToolId: null };
}

// ── hook ──────────────────────────────────────────────────────────────────────

export function useChat() {
  const [messages, dispatch] = useReducer(reducer, []);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const ctxRef = useRef<StreamCtx>(freshCtx());
  const seq = useRef(0);

  const nextId = () => String(++seq.current);

  // ── stream helpers (mutate ctxRef synchronously, dispatch for UI) ──

  function ensureThinking() {
    if (!ctxRef.current.thinkingId) {
      const id = nextId();
      ctxRef.current.thinkingId = id;
      dispatch({ type: "ADD", item: { id, kind: "thinking", text: "", done: false } });
    }
    return ctxRef.current.thinkingId;
  }

  function closeThinking() {
    if (ctxRef.current.thinkingId) {
      dispatch({ type: "CLOSE_THINKING", id: ctxRef.current.thinkingId });
      ctxRef.current.thinkingId = null;
    }
  }

  function ensureTimeline() {
    if (!ctxRef.current.timelineId) {
      const id = nextId();
      ctxRef.current.timelineId = id;
      ctxRef.current.pendingToolId = null;
      dispatch({ type: "ADD_TIMELINE", id });
    }
    return ctxRef.current.timelineId;
  }

  function closeTimeline() {
    ctxRef.current.timelineId = null;
    ctxRef.current.pendingToolId = null;
  }

  function closeContent() {
    ctxRef.current.contentId = null;
  }

  function ensureContent() {
    if (!ctxRef.current.contentId) {
      const id = nextId();
      ctxRef.current.contentId = id;
      dispatch({ type: "ADD", item: { id, kind: "assistant", text: "" } });
    }
    return ctxRef.current.contentId;
  }

  function onNodeChange(nodeId: string) {
    if (!nodeId || nodeId === ctxRef.current.lastNodeId) return;
    closeThinking();
    closeContent();
    closeTimeline();
    ctxRef.current.lastNodeId = nodeId;
  }

  function handleEvent(event: ChatEvent) {
    const { type, message = "", content = "", node_id: nodeId = "" } = event;

    switch (type) {
      case "thinking":
      case "planning": {
        const icon = type === "planning" ? "📋" : "●";
        if (message) {
          setProgress(message);
          if (nodeId && nodeId !== ctxRef.current.lastNodeId) {
            closeTimeline();
            ctxRef.current.lastNodeId = nodeId;
          }
          closeThinking();
          closeContent();
          const tid = ensureTimeline();
          dispatch({ type: "ADD_PHASE", timelineId: tid, entry: { kind: "phase", icon, text: message, cls: "tl-phase" } });
        }
        if (content) {
          onNodeChange(nodeId);
          const bid = ensureThinking();
          dispatch({ type: "APPEND_THINKING", id: bid, chunk: content });
        }
        break;
      }

      case "generating": {
        if (message && !content) {
          setProgress(message);
          if (nodeId && nodeId !== ctxRef.current.lastNodeId) {
            closeTimeline();
            ctxRef.current.lastNodeId = nodeId;
          }
          closeThinking();
          closeContent();
          const tid = ensureTimeline();
          dispatch({ type: "ADD_PHASE", timelineId: tid, entry: { kind: "phase", icon: "●", text: message, cls: "tl-phase" } });
        }
        if (content) {
          onNodeChange(nodeId);
          const cid = ensureContent();
          dispatch({ type: "APPEND_CONTENT", id: cid, chunk: content });
        }
        break;
      }

      case "calling_tool": {
        if (message) {
          setProgress(message);
          closeThinking();
          closeContent();
          const tid = ensureTimeline();
          const toolId = nextId();
          ctxRef.current.pendingToolId = toolId;
          dispatch({ type: "ADD_TOOL", timelineId: tid, toolId, name: message });
        }
        break;
      }

      case "tool_result": {
        const isErr = message.includes("失败");
        const ctx = ctxRef.current;
        if (ctx.pendingToolId && ctx.timelineId) {
          dispatch({
            type: "RESOLVE_TOOL",
            timelineId: ctx.timelineId,
            toolId: ctx.pendingToolId,
            status: isErr ? "err" : "ok",
            result: message || (isErr ? "✗" : "✓"),
          });
          ctx.pendingToolId = null;
        } else if (ctx.timelineId) {
          dispatch({
            type: "ADD_PHASE",
            timelineId: ctx.timelineId,
            entry: { kind: "phase", icon: isErr ? "✗" : "✓", text: message, cls: isErr ? "tl-err" : "tl-ok" },
          });
        }
        break;
      }

      case "verifying": {
        if (message) {
          setProgress(message);
          if (nodeId && nodeId !== ctxRef.current.lastNodeId) {
            closeTimeline();
            ctxRef.current.lastNodeId = nodeId;
          }
          closeThinking();
          closeContent();
          const tid = ensureTimeline();
          dispatch({ type: "ADD_PHASE", timelineId: tid, entry: { kind: "phase", icon: "🔍", text: message, cls: "tl-phase" } });
        }
        break;
      }

      case "finalizing": {
        if (message) {
          setProgress(message);
          if (nodeId && nodeId !== ctxRef.current.lastNodeId) {
            closeTimeline();
            ctxRef.current.lastNodeId = nodeId;
          }
          closeThinking();
          closeContent();
          const tid = ensureTimeline();
          dispatch({ type: "ADD_PHASE", timelineId: tid, entry: { kind: "phase", icon: "✨", text: message, cls: "tl-phase" } });
        }
        if (content) {
          onNodeChange(nodeId);
          const cid = ensureContent();
          dispatch({ type: "APPEND_CONTENT", id: cid, chunk: content });
        }
        break;
      }

      case "complete": {
        closeTimeline();
        setProgress(message || "完成");
        break;
      }

      case "error": {
        const id = nextId();
        dispatch({ type: "ADD", item: { id, kind: "error", text: message || "未知错误" } });
        break;
      }
    }
  }

  const sendMessage = useCallback(async (text: string) => {
    if (!text.trim() || running) return;

    const userId = nextId();
    dispatch({ type: "ADD", item: { id: userId, kind: "user", text } });

    setRunning(true);
    setProgress("启动中...");
    ctxRef.current = freshCtx();

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const resp = await fetch("/neo/chat", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ message: text }),
        signal: controller.signal,
      });

      if (!resp.ok) {
        const errText = await resp.text();
        const id = nextId();
        dispatch({ type: "ADD", item: { id, kind: "error", text: "请求失败: " + (errText || resp.statusText) } });
        setRunning(false);
        setProgress(null);
        abortRef.current = null;
        return;
      }

      const reader = resp.body!.getReader();
      const decoder = new TextDecoder();
      let buf = "";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const lines = buf.split("\n");
        buf = lines.pop()!;
        for (const line of lines) {
          if (!line.startsWith("data: ")) continue;
          try { handleEvent(JSON.parse(line.slice(6)) as ChatEvent); } catch { /* ignore */ }
        }
      }
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        const id = nextId();
        dispatch({ type: "ADD", item: { id, kind: "error", text: "连接错误: " + (err as Error).message } });
      }
    }

    closeThinking();
    closeTimeline();
    setRunning(false);
    abortRef.current = null;
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [running]);

  const stop = useCallback(() => {
    if (abortRef.current) {
      abortRef.current.abort();
      abortRef.current = null;
    }
    const id = nextId();
    dispatch({ type: "ADD", item: { id, kind: "stopped" } });
    closeThinking();
    closeTimeline();
    setRunning(false);
    setProgress(null);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadHistory = useCallback(async () => {
    try {
      const resp = await fetch("/neo/history");
      const json = await resp.json();
      const msgs: HistoryMessage[] = json.data ?? [];
      const items: MessageItem[] = [];
      for (const msg of msgs) {
        if (msg.role === "system") continue;
        const text = (msg.parts ?? [])
          .filter((p) => p.type === "text" && p.text)
          .map((p) => p.text!)
          .join("\n");
        if (!text) continue;
        const id = nextId();
        if (msg.role === "human") {
          items.push({ id, kind: "user", text });
        } else {
          items.push({ id, kind: "assistant", text });
        }
      }
      dispatch({ type: "SET", items });
    } catch { /* ignore */ }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { messages, running, progress, sendMessage, stop, loadHistory, renderMd };
}

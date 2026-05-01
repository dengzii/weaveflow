import { useReducer, useRef, useState, useCallback } from "react";
import type { ChatEvent, HistoryMessage, MessageItem } from "../types";
import { chatReducer, freshCtx, type StreamCtx } from "./chatReducer";

// ── hook ──────────────────────────────────────────────────────────────────────

export function useChat() {
  const [messages, dispatch] = useReducer(chatReducer, []);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const ctxRef = useRef<StreamCtx>(freshCtx());
  const seq = useRef(0);

  const nextId = () => String(++seq.current);

  // ── stream helpers ────────────────────────────────────────────────────────

  function completeLastStep() {
    const id = ctxRef.current.lastStepId;
    if (id) {
      dispatch({ type: "SET_STEP_DONE", id });
      ctxRef.current.lastStepId = null;
    }
  }

  function ensureThinking() {
    if (!ctxRef.current.thinkingId) {
      const id = nextId();
      ctxRef.current.thinkingId = id;
      dispatch({ type: "ADD", item: { id, kind: "thinking", text: "", done: false } });
    }
    return ctxRef.current.thinkingId!;
  }

  function closeThinking() {
    if (ctxRef.current.thinkingId) {
      dispatch({ type: "CLOSE_THINKING", id: ctxRef.current.thinkingId });
      ctxRef.current.thinkingId = null;
    }
  }

  function ensureContent() {
    if (!ctxRef.current.contentId) {
      const id = nextId();
      ctxRef.current.contentId = id;
      dispatch({ type: "ADD", item: { id, kind: "assistant", text: "" } });
    }
    return ctxRef.current.contentId!;
  }

  function closeContent() {
    ctxRef.current.contentId = null;
  }

  // ── event handler ─────────────────────────────────────────────────────────

  function handleEvent(event: ChatEvent) {
    const { type, content = "", data } = event;

    switch (type) {
      case "step_event": {
        completeLastStep();
        closeThinking();
        closeContent();
        if (content) {
          setProgress(content);
          const id = nextId();
          ctxRef.current.lastStepId = id;
          dispatch({ type: "ADD", item: { id, kind: "step", text: content, status: "pending" } });
        }
        break;
      }
      case "thinking_chunk": {
        if (content) {
          dispatch({ type: "APPEND_THINKING", id: ensureThinking(), chunk: content });
        }
        break;
      }
      case "generating_chunk": {
        if (content) {
          closeThinking();
          dispatch({ type: "APPEND_CONTENT", id: ensureContent(), chunk: content });
        }
        break;
      }
      case "tool_call": {
        completeLastStep();
        closeThinking();
        closeContent();
        if (content) {
          setProgress(content);
          const id = nextId();
          ctxRef.current.pendingToolId = id;
          dispatch({ type: "ADD", item: { id, kind: "tool", name: data?.name ?? content, status: "calling", result: "", detail: "" } });
        }
        break;
      }
      case "tool_result": {
        const ctx = ctxRef.current;
        if (ctx.pendingToolId) {
          const isErr = content.includes("失败");
          dispatch({
            type: "SET_TOOL_DONE",
            id: ctx.pendingToolId,
            status: isErr ? "error" : "done",
            result: content,
            detail: data?.result ?? "",
          });
          ctx.pendingToolId = null;
        }
        break;
      }
      case "complete": {
        completeLastStep();
        setProgress(content || "完成");
        break;
      }
      case "error": {
        completeLastStep();
        const id = nextId();
        dispatch({ type: "ADD", item: { id, kind: "error", text: content || "未知错误" } });
        break;
      }
    }
  }

  // ── sendMessage ───────────────────────────────────────────────────────────

  const sendMessage = useCallback(async (text: string) => {
    if (!text.trim() || running) return;

    dispatch({ type: "ADD", item: { id: nextId(), kind: "user", text } });
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
        dispatch({ type: "ADD", item: { id: nextId(), kind: "error", text: "请求失败: " + (errText || resp.statusText) } });
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
        dispatch({ type: "ADD", item: { id: nextId(), kind: "error", text: "连接错误: " + (err as Error).message } });
      }
    }

    completeLastStep();
    closeThinking();
    setRunning(false);
    abortRef.current = null;
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [running]);

  // ── stop ─────────────────────────────────────────────────────────────────

  const stop = useCallback(() => {
    if (abortRef.current) {
      abortRef.current.abort();
      abortRef.current = null;
    }
    completeLastStep();
    closeThinking();
    dispatch({ type: "ADD", item: { id: nextId(), kind: "stopped" } });
    setRunning(false);
    setProgress(null);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ── loadHistory ───────────────────────────────────────────────────────────

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
        items.push({ id: nextId(), kind: msg.role === "human" ? "user" : "assistant", text });
      }
      dispatch({ type: "SET", items });
    } catch { /* ignore */ }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { messages, running, progress, sendMessage, stop, loadHistory };
}

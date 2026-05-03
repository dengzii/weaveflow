import { useCallback, useReducer, useRef, useState } from "react";
import type { ChatEvent, HistoryMessage, HistoryPart, MessageItem } from "../types";
import { chatReducer, freshCtx, type StreamCtx } from "./chatReducer";

type ToolItem = Extract<MessageItem, { kind: "tool" }>;

type OrchestrationDecision = {
  mode?: string;
  reasoning?: string;
  direct_answer?: string;
};

type NormalizedInternalText = {
  text: string;
  directAnswer: string;
  internal: boolean;
};

function thinkingEventKey(data?: Record<string, string>): string | null {
  const stepId = data?.step_id?.trim();
  if (stepId) {
    return `step:${stepId}`;
  }

  const nodeId = data?.node_id?.trim();
  if (nodeId) {
    return `node:${nodeId}`;
  }

  return null;
}

function isToolError(text: string, explicitError?: string): boolean {
  return !!explicitError || /失败|failed/i.test(text);
}

function createToolItem(id: string, name: string, toolCallId?: string, args = ""): ToolItem {
  return {
    id,
    kind: "tool",
    toolCallId,
    name,
    status: "calling",
    args,
    output: "",
    error: "",
  };
}

function contentEventKey(data?: Record<string, string>): string | null {
  return thinkingEventKey(data);
}

function stripCodeFence(text: string): string {
  const trimmed = text.trim();
  if (!trimmed.startsWith("```")) {
    return trimmed;
  }

  return trimmed
    .replace(/^```json\s*/i, "")
    .replace(/^```\s*/, "")
    .replace(/\s*```$/, "")
    .trim();
}

function extractBalancedJSONObject(text: string, start: number): string | null {
  if (start < 0 || start >= text.length || text[start] !== "{") {
    return null;
  }

  let depth = 0;
  let inString = false;
  let escaped = false;
  for (let index = start; index < text.length; index += 1) {
    const ch = text[index];
    if (inString) {
      if (escaped) {
        escaped = false;
        continue;
      }
      if (ch === "\\") {
        escaped = true;
      } else if (ch === "\"") {
        inString = false;
      }
      continue;
    }

    if (ch === "\"") {
      inString = true;
      continue;
    }
    if (ch === "{") {
      depth += 1;
      continue;
    }
    if (ch === "}") {
      depth -= 1;
      if (depth === 0) {
        return text.slice(start, index + 1).trim();
      }
    }
  }

  return null;
}

function extractOrchestrationJSONObject(text: string): string | null {
  const trimmed = stripCodeFence(text);
  if (!trimmed) {
    return null;
  }

  if (trimmed.startsWith("{")) {
    const candidate = extractBalancedJSONObject(trimmed, 0);
    if (candidate) {
      return candidate;
    }
  }

  const markers = ['{"mode"', '{"direct_answer"', '{"reasoning"'];
  let best = -1;
  for (const marker of markers) {
    const index = trimmed.indexOf(marker);
    if (index >= 0 && (best < 0 || index < best)) {
      best = index;
    }
  }
  if (best < 0) {
    return null;
  }

  return extractBalancedJSONObject(trimmed, best);
}

function parseOrchestrationDecision(text: string): OrchestrationDecision | null {
  const candidate = extractOrchestrationJSONObject(text);
  if (!candidate) {
    return null;
  }

  try {
    const parsed = JSON.parse(candidate) as OrchestrationDecision;
    const mode = parsed.mode?.trim().toLowerCase();
    if (mode !== "direct" && mode !== "planner" && mode !== "supervisor") {
      return null;
    }

    const reasoning = parsed.reasoning?.trim() ?? "";
    const directAnswer = parsed.direct_answer?.trim() ?? "";
    if (!reasoning && !directAnswer) {
      return null;
    }

    return {
      mode,
      reasoning,
      direct_answer: directAnswer,
    };
  } catch {
    return null;
  }
}

function looksLikeOrchestrationFragment(text: string): boolean {
  const trimmed = stripCodeFence(text);
  if (!trimmed || !trimmed.startsWith("{")) {
    return false;
  }

  return (
    trimmed.includes('"mode"') ||
    trimmed.includes('"direct_answer"') ||
    trimmed.includes('"reasoning"')
  );
}

function normalizeThinkingText(text: string): NormalizedInternalText {
  const decision = parseOrchestrationDecision(text);
  if (decision) {
    return {
      text: decision.reasoning || (decision.direct_answer ? "Direct answer selected." : ""),
      directAnswer: decision.direct_answer ?? "",
      internal: true,
    };
  }

  if (looksLikeOrchestrationFragment(text)) {
    return {
      text: "Analyzing whether a direct answer is possible...",
      directAnswer: "",
      internal: true,
    };
  }

  return { text, directAnswer: "", internal: false };
}

function normalizeAssistantText(text: string): NormalizedInternalText {
  const decision = parseOrchestrationDecision(text);
  if (decision) {
    return {
      text: decision.direct_answer ?? "",
      directAnswer: decision.direct_answer ?? "",
      internal: true,
    };
  }

  if (looksLikeOrchestrationFragment(text)) {
    return { text: "", directAnswer: "", internal: true };
  }

  return { text, directAnswer: "", internal: false };
}

function normalizeHistoryMessage(message: HistoryMessage): HistoryMessage {
  if (message.role === "human" || message.role === "system") {
    return message;
  }

  const parts: HistoryPart[] = [];
  let pendingDirectAnswer = "";
  for (const part of message.parts ?? []) {
    if (part.type === "thinking" && part.text) {
      const normalized = normalizeThinkingText(part.text);
      if (normalized.directAnswer && !pendingDirectAnswer) {
        pendingDirectAnswer = normalized.directAnswer;
      }
      if (normalized.text) {
        parts.push({ ...part, text: normalized.text });
      }
      continue;
    }

    if (part.type === "text" && part.text) {
      const normalized = normalizeAssistantText(part.text);
      if (normalized.directAnswer && !pendingDirectAnswer) {
        pendingDirectAnswer = normalized.directAnswer;
      }
      if (normalized.text) {
        parts.push({ ...part, text: normalized.text });
      }
      continue;
    }

    parts.push(part);
  }

  if (pendingDirectAnswer && !parts.some((part) => part.type === "text" && part.text?.trim() === pendingDirectAnswer)) {
    parts.push({ type: "text", text: pendingDirectAnswer });
  }

  return { ...message, parts };
}

function applyHistoryPart(
  items: MessageItem[],
  part: HistoryPart,
  nextId: () => string,
  toolItemIds: Map<string, string>,
) {
  switch (part.type) {
    case "step":
      if (part.text) {
        items.push({ id: nextId(), kind: "step", text: part.text, status: "done" });
      }
      return;

    case "thinking":
      if (part.text) {
        items.push({ id: nextId(), kind: "thinking", text: part.text, done: true });
      }
      return;

    case "tool_call": {
      const item = createToolItem(nextId(), part.name ?? "tool", part.id, part.text ?? "");
      items.push(item);
      if (part.id) {
        toolItemIds.set(part.id, item.id);
      }
      return;
    }

    case "tool_result": {
      const detail = part.result ?? "";
      const failed = isToolError(detail);
      const existingId = part.id ? toolItemIds.get(part.id) : undefined;
      if (!existingId) {
        items.push({
          ...createToolItem(nextId(), part.name ?? "tool", part.id),
          status: failed ? "error" : "done",
          output: failed ? "" : detail,
          error: failed ? detail : "",
        });
        return;
      }

      const index = items.findIndex((item) => item.id === existingId && item.kind === "tool");
      if (index < 0) {
        return;
      }

      const current = items[index];
      if (current.kind !== "tool") {
        return;
      }

      items[index] = {
        ...current,
        name: part.name ?? current.name,
        status: failed ? "error" : "done",
        output: failed ? "" : detail,
        error: failed ? detail : "",
      };
      return;
    }

    case "text":
      if (part.text) {
        items.push({ id: nextId(), kind: "assistant", text: part.text });
      }
      return;

    default:
      return;
  }
}

function buildHistoryItems(messages: HistoryMessage[], nextId: () => string): MessageItem[] {
  const items: MessageItem[] = [];

  for (const rawMessage of messages) {
    const message = normalizeHistoryMessage(rawMessage);
    if (message.role === "system") {
      continue;
    }

    if (message.role === "human") {
      const text = (message.parts ?? [])
        .filter((part) => part.type === "text" && part.text)
        .map((part) => part.text!)
        .join("\n");
      if (text) {
        items.push({ id: nextId(), kind: "user", text });
      }
      continue;
    }

    const toolItemIds = new Map<string, string>();
    for (const part of message.parts ?? []) {
      applyHistoryPart(items, part, nextId, toolItemIds);
    }

    if (message.status === "failed") {
      items.push({ id: nextId(), kind: "error", text: "执行失败" });
    } else if (message.status === "stopped") {
      items.push({ id: nextId(), kind: "stopped" });
    }
  }

  return items;
}

export function useChat() {
  const [messages, dispatch] = useReducer(chatReducer, []);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const ctxRef = useRef<StreamCtx>(freshCtx());
  const seq = useRef(0);

  const nextId = () => String(++seq.current);

  function completeLastStep() {
    const id = ctxRef.current.lastStepId;
    if (id) {
      dispatch({ type: "SET_STEP_DONE", id });
      ctxRef.current.lastStepId = null;
    }
  }

  function ensureThinking(key?: string | null) {
    if (key) {
      const existingId = ctxRef.current.thinkingIdsByKey[key];
      if (existingId) {
        return existingId;
      }
    }

    if (!ctxRef.current.thinkingId) {
      const id = nextId();
      ctxRef.current.thinkingId = id;
      dispatch({ type: "ADD", item: { id, kind: "thinking", text: "", done: false } });
    }

    const id = ctxRef.current.thinkingId!;
    if (key) {
      ctxRef.current.thinkingIdsByKey[key] = id;
    }
    return id;
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
    ctxRef.current.contentKey = null;
    ctxRef.current.contentRaw = "";
  }

  function setContentText(text: string) {
    const normalized = text.trim();
    if (!normalized) {
      return;
    }

    const id = ensureContent();
    ctxRef.current.contentRaw = normalized;
    ctxRef.current.assistantShown = true;
    dispatch({ type: "SET_CONTENT_TEXT", id, text: normalized });
  }

  function flushPendingDirectAnswer() {
    const answer = ctxRef.current.pendingDirectAnswer.trim();
    if (!answer || ctxRef.current.assistantShown) {
      return;
    }

    closeThinking();
    setContentText(answer);
  }

  function syncThinkingText(chunk: string, data?: Record<string, string>) {
    const id = ensureThinking(thinkingEventKey(data));
    const raw = (ctxRef.current.thinkingRawById[id] ?? "") + chunk;
    ctxRef.current.thinkingRawById[id] = raw;

    const normalized = normalizeThinkingText(raw);
    if (normalized.directAnswer) {
      ctxRef.current.pendingDirectAnswer = normalized.directAnswer;
    }
    dispatch({ type: "SET_THINKING_TEXT", id, text: normalized.text });
  }

  function syncContentText(chunk: string, data?: Record<string, string>) {
    const key = contentEventKey(data);
    if (key && ctxRef.current.contentKey !== key) {
      ctxRef.current.contentKey = key;
      ctxRef.current.contentRaw = "";
    }

    const raw = ctxRef.current.contentRaw + chunk;
    ctxRef.current.contentRaw = raw;

    const normalized = normalizeAssistantText(raw);
    if (normalized.directAnswer) {
      ctxRef.current.pendingDirectAnswer = normalized.directAnswer;
    }
    if (!normalized.text) {
      return;
    }

    closeThinking();
    setContentText(normalized.text);
  }

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
        completeLastStep();
        if (content) {
          syncThinkingText(content, data);
        }
        break;
      }

      case "generating_chunk": {
        completeLastStep();
        if (content) {
          syncContentText(content, data);
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
          const toolCallId = data?.tool_call_id ?? id;
          ctxRef.current.pendingToolIds[toolCallId] = id;
          dispatch({
            type: "ADD",
            item: createToolItem(id, data?.name ?? content, toolCallId, data?.arguments ?? ""),
          });
        }
        break;
      }

      case "tool_result": {
        const toolCallId = data?.tool_call_id;
        let itemId = toolCallId ? ctxRef.current.pendingToolIds[toolCallId] : undefined;
        if (!itemId) {
          itemId = nextId();
          dispatch({
            type: "ADD",
            item: createToolItem(itemId, data?.name ?? "tool", toolCallId),
          });
        }

        const detail = data?.result ?? data?.error ?? content;
        const failed = isToolError(detail, data?.error);
        dispatch({
          type: "SET_TOOL_DONE",
          id: itemId,
          status: failed ? "error" : "done",
          output: failed ? "" : detail,
          error: failed ? detail : "",
        });

        if (toolCallId) {
          delete ctxRef.current.pendingToolIds[toolCallId];
        }
        setProgress(content || null);
        break;
      }

      case "complete": {
        completeLastStep();
        flushPendingDirectAnswer();
        closeThinking();
        closeContent();
        setProgress(content || "完成");
        break;
      }

      case "error": {
        completeLastStep();
        closeThinking();
        closeContent();
        const id = nextId();
        dispatch({ type: "ADD", item: { id, kind: "error", text: content || "未知错误" } });
        break;
      }
    }
  }

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
      if (buf.startsWith("data: ")) {
        try { handleEvent(JSON.parse(buf.slice(6)) as ChatEvent); } catch { /* ignore */ }
      }
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        dispatch({ type: "ADD", item: { id: nextId(), kind: "error", text: "连接错误: " + (err as Error).message } });
      }
    }

    flushPendingDirectAnswer();
    completeLastStep();
    closeThinking();
    closeContent();
    setRunning(false);
    abortRef.current = null;
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [running]);

  const stop = useCallback(() => {
    if (abortRef.current) {
      abortRef.current.abort();
      abortRef.current = null;
    }
    completeLastStep();
    closeThinking();
    closeContent();
    dispatch({ type: "ADD", item: { id: nextId(), kind: "stopped" } });
    setRunning(false);
    setProgress(null);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadHistory = useCallback(async () => {
    try {
      const resp = await fetch("/neo/history");
      const json = await resp.json();
      const history: HistoryMessage[] = json.data ?? [];
      dispatch({ type: "SET", items: buildHistoryItems(history, nextId) });
    } catch { /* ignore */ }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { messages, running, progress, sendMessage, stop, loadHistory };
}

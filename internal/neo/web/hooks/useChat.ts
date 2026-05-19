import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { apiAction, apiFetch } from "../api";
import type { ApiResponse, ChatEvent, ClarificationQuestion, HistoryMessage, HistoryPart, MemoryEntry, MessageItem, PlanProgress, PlanProgressStep } from "../types";
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

function dataString(data: Record<string, unknown> | undefined, key: string): string {
  const value = data?.[key];
  return typeof value === "string" ? value : "";
}

function thinkingEventKey(data?: Record<string, unknown>): string | null {
  const stepId = dataString(data, "step_id").trim();
  if (stepId) {
    return `step:${stepId}`;
  }

  const nodeId = dataString(data, "node_id").trim();
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

function contentEventKey(data?: Record<string, unknown>): string | null {
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

function asString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function asNumber(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function parseClarification(data?: Record<string, unknown>, content = ""): ClarificationQuestion | null {
  if (!data || data.kind !== "clarification_question") {
    return null;
  }
  const phase = asString(data.phase);
  if (phase && phase !== "pending") {
    return null;
  }

  const rawOptions = Array.isArray(data.options) ? data.options : [];
  const options: string[] = rawOptions
    .map((item) => asString(item).trim())
    .filter((item) => item.length > 0);

  const question = asString(data.question) || content;
  return {
    question,
    options,
    reasoning: asString(data.reasoning) || undefined,
    attempts: typeof data.attempts === "number" ? data.attempts : undefined,
  };
}

function parsePlanProgress(data?: Record<string, unknown>, content = ""): PlanProgress | null {
  if (!data || data.kind !== "planner_progress") {
    return null;
  }

  const rawCounts = typeof data.counts === "object" && data.counts !== null ? data.counts as Record<string, unknown> : {};
  const rawSteps = Array.isArray(data.steps) ? data.steps : [];
  const steps: PlanProgressStep[] = rawSteps
    .filter((item): item is Record<string, unknown> => typeof item === "object" && item !== null)
    .map((item) => ({
      id: asString(item.id),
      title: asString(item.title) || asString(item.id),
      description: asString(item.description),
      status: asString(item.status) || "pending",
      kind: asString(item.kind),
    }));

  const currentRaw = typeof data.current_step === "object" && data.current_step !== null
    ? data.current_step as Record<string, unknown>
    : null;

  return {
    phase: asString(data.phase),
    message: asString(data.message) || content,
    planner_path: asString(data.planner_path),
    objective: asString(data.objective),
    status: asString(data.status),
    summary: asString(data.summary),
    replan_reason: asString(data.replan_reason),
    current_step_id: asString(data.current_step_id),
    current_step: currentRaw ? {
      id: asString(currentRaw.id),
      title: asString(currentRaw.title) || asString(currentRaw.id),
      description: asString(currentRaw.description),
      status: asString(currentRaw.status) || "pending",
      kind: asString(currentRaw.kind),
    } : null,
    steps,
    counts: {
      total: asNumber(rawCounts.total),
      pending: asNumber(rawCounts.pending),
      ready: asNumber(rawCounts.ready),
      in_progress: asNumber(rawCounts.in_progress),
      completed: asNumber(rawCounts.completed),
      blocked: asNumber(rawCounts.blocked),
      skipped: asNumber(rawCounts.skipped),
    },
    percent: Math.max(0, Math.min(100, Math.round(asNumber(data.percent)))),
  };
}

export function useChat() {
  const [messages, dispatch] = useReducer(chatReducer, []);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<string | null>(null);
  const [planProgress, setPlanProgress] = useState<PlanProgress | null>(null);
  const [clarification, setClarification] = useState<ClarificationQuestion | null>(null);
  const [resumable, setResumable] = useState(false);

  useEffect(() => {
    void (async () => {
      try {
        const json = await apiFetch<ApiResponse<{ resumable: boolean; paused: boolean }>>("/neo/status");
        setResumable(Boolean(json.data?.resumable && !json.data?.paused));
      } catch { /* ignore */ }
    })();
  }, []);
  const abortRef = useRef<AbortController | null>(null);
  const ctxRef = useRef<StreamCtx>(freshCtx());
  const terminalEventSeenRef = useRef(false);
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

  function syncThinkingText(chunk: string, data?: Record<string, unknown>) {
    const id = ensureThinking(thinkingEventKey(data));
    const raw = (ctxRef.current.thinkingRawById[id] ?? "") + chunk;
    ctxRef.current.thinkingRawById[id] = raw;

    const normalized = normalizeThinkingText(raw);
    if (normalized.directAnswer) {
      ctxRef.current.pendingDirectAnswer = normalized.directAnswer;
    }
    dispatch({ type: "SET_THINKING_TEXT", id, text: normalized.text });
  }

  function syncContentText(chunk: string, data?: Record<string, unknown>) {
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
          const toolCallId = dataString(data, "tool_call_id") || id;
          ctxRef.current.pendingToolIds[toolCallId] = id;
          dispatch({
            type: "ADD",
            item: createToolItem(id, dataString(data, "name") || content, toolCallId, dataString(data, "arguments")),
          });
        }
        break;
      }

      case "tool_result": {
        const toolCallId = dataString(data, "tool_call_id");
        let itemId = toolCallId ? ctxRef.current.pendingToolIds[toolCallId] : undefined;
        if (!itemId) {
          itemId = nextId();
          dispatch({
            type: "ADD",
            item: createToolItem(itemId, dataString(data, "name") || "tool", toolCallId),
          });
        }

        const detail = dataString(data, "result") || dataString(data, "error") || content;
        const failed = isToolError(detail, dataString(data, "error"));
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

      case "planner_progress": {
        const next = parsePlanProgress(data, content);
        if (next) {
          setPlanProgress(next);
          setProgress(next.message || next.summary || next.status || content || null);
        }
        break;
      }

      case "clarification_question": {
        const phase = asString(data?.phase);
        if (phase === "resolved" || phase === "exhausted") {
          setClarification(null);
          break;
        }
        const next = parseClarification(data, content);
        if (next) {
          setClarification(next);
          setProgress(next.question || content || null);
        }
        break;
      }

      case "complete": {
        terminalEventSeenRef.current = true;
        completeLastStep();
        flushPendingDirectAnswer();
        closeThinking();
        closeContent();
        setProgress(content || "完成");
        break;
      }

      case "error": {
        terminalEventSeenRef.current = true;
        completeLastStep();
        closeThinking();
        closeContent();
        const id = nextId();
        dispatch({ type: "ADD", item: { id, kind: "error", text: content || "未知错误" } });
        break;
      }
    }
  }

  async function streamSSE(resp: Response, controller: AbortController) {
    if (!resp.ok) {
      const errText = await resp.text();
      dispatch({ type: "ADD", item: { id: nextId(), kind: "error", text: "请求失败: " + (errText || resp.statusText) } });
      return;
    }
    if (!resp.body) {
      dispatch({ type: "ADD", item: { id: nextId(), kind: "error", text: "响应流不可用" } });
      return;
    }
    const reader = resp.body.getReader();
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
    if (!controller.signal.aborted && !terminalEventSeenRef.current) {
      dispatch({
        type: "ADD",
        item: { id: nextId(), kind: "error", text: "连接中断：响应流在完成前被关闭" },
      });
    }
  }

  async function refreshResumableStatus(retries = 0): Promise<void> {
    try {
      const json = await apiFetch<ApiResponse<{ paused: boolean; resumable: boolean; run_id?: string; status?: string }>>("/neo/status");
      const data = json.data;
      const resumable = Boolean(data?.resumable && !data?.paused);
      setResumable(resumable);
      if (!resumable && retries > 0) {
        await new Promise((resolve) => setTimeout(resolve, 400));
        return refreshResumableStatus(retries - 1);
      }
    } catch { /* ignore */ }
  }

  const sendMessage = useCallback(async (text: string) => {
    if (!text.trim() || running) return;

    dispatch({ type: "ADD", item: { id: nextId(), kind: "user", text } });
    setRunning(true);
    setProgress("启动中...");
    setPlanProgress(null);
    setClarification(null);
    setResumable(false);
    ctxRef.current = freshCtx();
    terminalEventSeenRef.current = false;

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const resp = await fetch("/neo/chat", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ message: text }),
        signal: controller.signal,
      });
      await streamSSE(resp, controller);
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
    void refreshResumableStatus(3);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [running]);

  const resumeRun = useCallback(async () => {
    if (running) return;
    setRunning(true);
    setProgress("正在恢复...");
    setPlanProgress(null);
    setClarification(null);
    setResumable(false);
    ctxRef.current = freshCtx();
    terminalEventSeenRef.current = false;

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const resp = await fetch("/neo/resume", {
        method: "POST",
        signal: controller.signal,
      });
      await streamSSE(resp, controller);
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        dispatch({ type: "ADD", item: { id: nextId(), kind: "error", text: "恢复失败: " + (err as Error).message } });
      }
    }

    flushPendingDirectAnswer();
    completeLastStep();
    closeThinking();
    closeContent();
    setRunning(false);
    abortRef.current = null;
    void refreshResumableStatus(3);
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
    void refreshResumableStatus(5);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadHistory = useCallback(async () => {
    try {
      const json = await apiFetch<ApiResponse<HistoryMessage[]>>("/neo/history");
      const history: HistoryMessage[] = json.data ?? [];
      dispatch({ type: "SET", items: buildHistoryItems(history, nextId) });
    } catch { /* ignore */ }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const clearHistory = useCallback(async () => {
    await apiAction("/neo/history", { method: "DELETE" });
    ctxRef.current = freshCtx();
    dispatch({ type: "SET", items: [] });
    setProgress(null);
    setPlanProgress(null);
    setClarification(null);
    setResumable(false);
  }, []);

  const loadMemory = useCallback(async () => {
    const json = await apiFetch<ApiResponse<MemoryEntry[]>>("/neo/memory");
    return json.data ?? [];
  }, []);

  const clearMemory = useCallback(async () => {
    await apiAction("/neo/memory", { method: "DELETE" });
  }, []);

  return { messages, running, progress, planProgress, clarification, resumable, sendMessage, resumeRun, stop, loadHistory, clearHistory, loadMemory, clearMemory };
}

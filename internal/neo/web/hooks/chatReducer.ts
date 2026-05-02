import type { MessageItem } from "../types";

export type Action =
  | { type: "SET"; items: MessageItem[] }
  | { type: "ADD"; item: MessageItem }
  | { type: "CLOSE_THINKING"; id: string }
  | { type: "APPEND_THINKING"; id: string; chunk: string }
  | { type: "APPEND_CONTENT"; id: string; chunk: string }
  | { type: "SET_STEP_DONE"; id: string }
  | { type: "SET_TOOL_DONE"; id: string; status: "done" | "error"; output: string; error: string };

export function chatReducer(state: MessageItem[], action: Action): MessageItem[] {
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

    case "APPEND_CONTENT":
      return state.map((m) =>
        m.id === action.id && m.kind === "assistant"
          ? { ...m, text: m.text + action.chunk }
          : m
      );

    case "SET_STEP_DONE":
      return state.map((m) =>
        m.id === action.id && m.kind === "step" ? { ...m, status: "done" as const } : m
      );

    case "SET_TOOL_DONE":
      return state.map((m) =>
        m.id === action.id && m.kind === "tool"
          ? { ...m, status: action.status, output: action.output, error: action.error }
          : m
      );

    default:
      return state;
  }
}

export interface StreamCtx {
  lastStepId: string | null;
  pendingToolIds: Record<string, string>;
  thinkingIdsByKey: Record<string, string>;
  thinkingId: string | null;
  contentId: string | null;
}

export function freshCtx(): StreamCtx {
  return {
    lastStepId: null,
    pendingToolIds: {},
    thinkingIdsByKey: {},
    thinkingId: null,
    contentId: null,
  };
}

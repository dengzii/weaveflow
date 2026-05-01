export interface Config {
  system_prompt: string;
  max_iterations: number;
  planner_max_steps: number;
  memory_recall_limit: number;
  tools: Record<string, boolean>;
  mode: "auto" | "direct" | "planner";
}

export interface ApiResponse<T> {
  code: number;
  data: T;
}

export interface HistoryPart {
  type: string;
  text?: string;
  name?: string;
  result?: unknown;
}

export interface HistoryMessage {
  role: string;
  parts: HistoryPart[];
}

export interface ChatEvent {
  type: string;
  content?: string;
  data?: Record<string, string>;
}

export type MessageItem =
  | { id: string; kind: "user"; text: string }
  | { id: string; kind: "step"; text: string; status: "pending" | "done" }
  | { id: string; kind: "thinking"; text: string; done: boolean }
  | { id: string; kind: "tool"; name: string; status: "calling" | "done" | "error"; result: string; detail: string }
  | { id: string; kind: "assistant"; text: string }
  | { id: string; kind: "error"; text: string }
  | { id: string; kind: "stopped" };

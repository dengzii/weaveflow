import { useCallback, useEffect, useRef, useState } from "react";
import { managementHeaders, resolveBackendUrl } from "../../lib/backend";
import { isPlainRecord } from "../../lib/utils";
import type { RuntimeEvent } from "../../types";

export type RuntimeEventStreamStatus =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "gap"
  | "failed"
  | "closed";

export interface RuntimeEventStreamGap {
  type: "stream.gap";
  graph_id: string;
  requested_cursor: string;
  oldest_event_id?: string;
  resume_cursor?: string;
  reason: string;
  recoverable_events: "persistent_only";
}

export interface RuntimeEventStreamDiagnostics {
  lastEventID: string;
  retryAttempt: number;
  retryDelayMS: number;
  lastErrorKind: "" | "cursor_gap" | "client_error" | "server_error" | "network_error" | "stream_ended";
  lastError: string;
  receivedEvents: number;
  discardedFrames: number;
  receivedEventsPerSecond: number;
  discardedFramesPerSecond: number;
}

export interface RuntimeEventStreamState {
  status: RuntimeEventStreamStatus;
  diagnostics: RuntimeEventStreamDiagnostics;
}

export interface RuntimeEventStreamController extends RuntimeEventStreamState {
  reconnect: () => void;
}

type RuntimeEventStreamFrame = RuntimeEvent | RuntimeEventStreamGap;

interface ParsedSSEFrame {
  data: string;
  eventID: string;
}

interface RuntimeEventStreamClientOptions {
  onEvent: (event: RuntimeEvent) => void;
  onGap: (gap: RuntimeEventStreamGap) => void;
  onState: (state: RuntimeEventStreamState) => void;
  fetcher?: typeof fetch;
  random?: () => number;
  setTimer?: (callback: () => void, delay: number) => number;
  clearTimer?: (timer: number) => void;
}

const initialDiagnostics: RuntimeEventStreamDiagnostics = {
  lastEventID: "",
  retryAttempt: 0,
  retryDelayMS: 0,
  lastErrorKind: "",
  lastError: "",
  receivedEvents: 0,
  discardedFrames: 0,
  receivedEventsPerSecond: 0,
  discardedFramesPerSecond: 0,
};

export function parseRuntimeEventFrame(data: string): RuntimeEventStreamFrame | null {
  try {
    const parsed = JSON.parse(data) as unknown;
    if (!isPlainRecord(parsed) || typeof parsed.type !== "string") return null;
    if (parsed.type === "stream.gap") {
      if (
        typeof parsed.graph_id !== "string" ||
        typeof parsed.requested_cursor !== "string" ||
        typeof parsed.reason !== "string" ||
        parsed.recoverable_events !== "persistent_only"
      ) {
        return null;
      }
      return parsed as unknown as RuntimeEventStreamGap;
    }
    if (
      typeof parsed.id !== "string" ||
      typeof parsed.graph_id !== "string" ||
      typeof parsed.run_id !== "string" ||
      typeof parsed.timestamp !== "string"
    ) {
      return null;
    }
    return parsed as unknown as RuntimeEvent;
  } catch {
    return null;
  }
}

export function reconnectDelayMS(attempt: number, random: () => number = Math.random): number {
  const exponential = Math.min(30_000, 1_000 * 2 ** Math.max(0, attempt - 1));
  return Math.min(30_000, Math.round(exponential * (0.8 + random() * 0.4)));
}

export class SSEFrameDecoder {
  private buffer = "";

  push(chunk: string): ParsedSSEFrame[] {
    this.buffer += chunk;
    const frames: ParsedSSEFrame[] = [];
    while (true) {
      const boundary = this.buffer.match(/\r?\n\r?\n/);
      if (!boundary || boundary.index === undefined) break;
      const block = this.buffer.slice(0, boundary.index);
      this.buffer = this.buffer.slice(boundary.index + boundary[0].length);
      const frame = parseSSEBlock(block);
      if (frame) frames.push(frame);
    }
    return frames;
  }
}

export class RuntimeEventStreamClient {
  private readonly onEvent: (event: RuntimeEvent) => void;
  private readonly onGap: (gap: RuntimeEventStreamGap) => void;
  private readonly onState: (state: RuntimeEventStreamState) => void;
  private readonly fetcher: typeof fetch;
  private readonly random: () => number;
  private readonly setTimer: (callback: () => void, delay: number) => number;
  private readonly clearTimer: (timer: number) => void;
  private graphID = "";
  private cursor = "";
  private generation = 0;
  private retryAttempt = 0;
  private retryTimer: number | null = null;
  private diagnosticsTimer: number | null = null;
  private controller: AbortController | null = null;
  private status: RuntimeEventStreamStatus = "closed";
  private diagnostics: RuntimeEventStreamDiagnostics = { ...initialDiagnostics };
  private sampledReceivedEvents = 0;
  private sampledDiscardedFrames = 0;

  constructor(options: RuntimeEventStreamClientOptions) {
    this.onEvent = options.onEvent;
    this.onGap = options.onGap;
    this.onState = options.onState;
    this.fetcher = options.fetcher ?? ((input, init) => globalThis.fetch(input, init));
    this.random = options.random ?? Math.random;
    this.setTimer = options.setTimer ?? ((callback, delay) => window.setTimeout(callback, delay));
    this.clearTimer = options.clearTimer ?? ((timer) => window.clearTimeout(timer));
  }

  start(graphID: string): void {
    this.stop(false);
    this.graphID = graphID.trim();
    this.cursor = "";
    this.retryAttempt = 0;
    this.diagnostics = { ...initialDiagnostics };
    this.sampledReceivedEvents = 0;
    this.sampledDiscardedFrames = 0;
    if (!this.graphID) {
      this.setStatus("closed");
      return;
    }
    this.setStatus("connecting");
    void this.connect(this.generation);
  }

  stop(emitState = true): void {
    this.generation += 1;
    this.clearRetry();
    if (this.diagnosticsTimer !== null) {
      this.clearTimer(this.diagnosticsTimer);
      this.diagnosticsTimer = null;
    }
    this.controller?.abort();
    this.controller = null;
    if (emitState) this.setStatus("closed");
  }

  reconnectNow(): void {
    if (!this.graphID) return;
    this.clearRetry();
    this.controller?.abort();
    this.controller = null;
    this.retryAttempt = 0;
    this.diagnostics.retryAttempt = 0;
    this.diagnostics.retryDelayMS = 0;
    this.diagnostics.lastErrorKind = "";
    this.diagnostics.lastError = "";
    this.setStatus("connecting");
    void this.connect(this.generation);
  }

  private async connect(generation: number): Promise<void> {
    if (generation !== this.generation || !this.graphID) return;
    const query = new URLSearchParams();
    if (this.cursor) query.set("cursor", this.cursor);
    const suffix = query.size > 0 ? `?${query.toString()}` : "";
    const controller = new AbortController();
    this.controller = controller;
    try {
      const response = await this.fetcher(
        resolveBackendUrl(`/graphs/${encodeURIComponent(this.graphID)}/events/stream${suffix}`),
        {
          headers: managementHeaders({ Accept: "text/event-stream" }),
          signal: controller.signal,
        }
      );
      if (!this.isCurrent(generation, controller)) return;
      if (response.status === 409 || response.status === 410) {
        const gap: RuntimeEventStreamGap = {
          type: "stream.gap",
          graph_id: this.graphID,
          requested_cursor: this.cursor,
          reason: `http_${response.status}`,
          recoverable_events: "persistent_only",
        };
        this.cursor = "";
        this.diagnostics.lastErrorKind = "cursor_gap";
        this.diagnostics.lastError = gap.reason;
        this.setStatus("gap");
        this.onGap(gap);
        this.scheduleReconnect(generation, "cursor_gap", gap.reason, true);
        return;
      }
      if (response.status >= 400 && response.status < 500) {
        this.diagnostics.lastErrorKind = "client_error";
        this.diagnostics.lastError = `HTTP ${response.status}`;
        this.setStatus("failed");
        return;
      }
      if (!response.ok) {
        this.scheduleReconnect(generation, "server_error", `HTTP ${response.status}`);
        return;
      }
      const contentType = response.headers.get("Content-Type")?.toLowerCase() ?? "";
      if (!contentType.includes("text/event-stream") || !response.body) {
        this.diagnostics.lastErrorKind = "client_error";
        this.diagnostics.lastError = "runtime event response is not an event stream";
        this.setStatus("failed");
        return;
      }
      this.retryAttempt = 0;
      this.diagnostics.retryAttempt = 0;
      this.diagnostics.retryDelayMS = 0;
      this.diagnostics.lastErrorKind = "";
      this.diagnostics.lastError = "";
      this.setStatus("connected");
      await this.consume(response.body, generation, controller);
      if (this.isCurrent(generation, controller)) {
        this.scheduleReconnect(generation, "stream_ended", "runtime event stream ended");
      }
    } catch (error) {
      if (!this.isCurrent(generation, controller) || isAbortError(error)) return;
      this.scheduleReconnect(generation, "network_error", errorMessage(error));
    }
  }

  private async consume(
    body: ReadableStream<Uint8Array>,
    generation: number,
    controller: AbortController
  ): Promise<void> {
    const reader = body.getReader();
    const textDecoder = new TextDecoder();
    const frameDecoder = new SSEFrameDecoder();
    try {
      while (this.isCurrent(generation, controller)) {
        const { done, value } = await reader.read();
        if (done) break;
        const frames = frameDecoder.push(textDecoder.decode(value, { stream: true }));
        for (const frame of frames) {
          if (!this.isCurrent(generation, controller)) return;
          this.handleFrame(frame);
        }
      }
    } finally {
      reader.releaseLock();
    }
  }

  private handleFrame(frame: ParsedSSEFrame): void {
    const parsed = parseRuntimeEventFrame(frame.data);
    if (!parsed) {
      this.diagnostics.discardedFrames += 1;
      this.scheduleDiagnostics();
      return;
    }
    if (parsed.type === "stream.gap") {
      this.cursor = parsed.resume_cursor ?? "";
      this.diagnostics.lastEventID = this.cursor;
      this.diagnostics.lastErrorKind = "cursor_gap";
      this.diagnostics.lastError = parsed.reason;
      this.setStatus("gap");
      this.onGap(parsed);
      return;
    }
    this.cursor = frame.eventID || parsed.id || this.cursor;
    this.diagnostics.lastEventID = this.cursor;
    this.diagnostics.receivedEvents += 1;
    if (this.status === "gap") {
      this.diagnostics.lastErrorKind = "";
      this.diagnostics.lastError = "";
      this.setStatus("connected");
    }
    this.scheduleDiagnostics();
    this.onEvent(parsed);
  }

  private scheduleReconnect(
    generation: number,
    kind: RuntimeEventStreamDiagnostics["lastErrorKind"],
    message: string,
    preserveStatus = false
  ): void {
    if (generation !== this.generation) return;
    this.controller = null;
    this.retryAttempt += 1;
    const delay = reconnectDelayMS(this.retryAttempt, this.random);
    this.diagnostics.retryAttempt = this.retryAttempt;
    this.diagnostics.retryDelayMS = delay;
    this.diagnostics.lastErrorKind = kind;
    this.diagnostics.lastError = message;
    if (preserveStatus) {
      this.emitState();
    } else {
      this.setStatus("reconnecting");
    }
    if (this.retryTimer !== null) this.clearTimer(this.retryTimer);
    this.retryTimer = this.setTimer(() => {
      this.retryTimer = null;
      if (generation !== this.generation) return;
      this.diagnostics.retryDelayMS = 0;
      this.setStatus("connecting");
      void this.connect(generation);
    }, delay);
  }

  private clearRetry(): void {
    if (this.retryTimer === null) return;
    this.clearTimer(this.retryTimer);
    this.retryTimer = null;
  }

  private scheduleDiagnostics(): void {
    if (this.diagnosticsTimer !== null) return;
    this.diagnosticsTimer = this.setTimer(() => {
      this.diagnosticsTimer = null;
      this.diagnostics.receivedEventsPerSecond =
        this.diagnostics.receivedEvents - this.sampledReceivedEvents;
      this.diagnostics.discardedFramesPerSecond =
        this.diagnostics.discardedFrames - this.sampledDiscardedFrames;
      this.sampledReceivedEvents = this.diagnostics.receivedEvents;
      this.sampledDiscardedFrames = this.diagnostics.discardedFrames;
      this.emitState();
    }, 1_000);
  }

  private setStatus(status: RuntimeEventStreamStatus): void {
    this.status = status;
    this.emitState();
  }

  private emitState(): void {
    this.onState({
      status: this.status,
      diagnostics: { ...this.diagnostics },
    });
  }

  private isCurrent(generation: number, controller: AbortController): boolean {
    return generation === this.generation && this.controller === controller && !controller.signal.aborted;
  }
}

export function useRuntimeEventStream(
  graphID: string,
  onEvent: (event: RuntimeEvent) => void,
  onGap: (gap: RuntimeEventStreamGap) => void
): RuntimeEventStreamController {
  const [state, setState] = useState<RuntimeEventStreamState>({
    status: graphID ? "connecting" : "closed",
    diagnostics: { ...initialDiagnostics },
  });
  const onEventRef = useRef(onEvent);
  const onGapRef = useRef(onGap);
  const clientRef = useRef<RuntimeEventStreamClient | null>(null);
  onEventRef.current = onEvent;
  onGapRef.current = onGap;

  const reconnect = useCallback(() => {
    clientRef.current?.reconnectNow();
  }, []);

  useEffect(() => {
    const client = new RuntimeEventStreamClient({
      onEvent: (event) => onEventRef.current(event),
      onGap: (gap) => onGapRef.current(gap),
      onState: setState,
    });
    clientRef.current = client;
    client.start(graphID);
    return () => {
      if (clientRef.current === client) clientRef.current = null;
      client.stop(false);
    };
  }, [graphID]);

  return { ...state, reconnect };
}

function parseSSEBlock(block: string): ParsedSSEFrame | null {
  let eventID = "";
  const data: string[] = [];
  for (const line of block.split(/\r?\n/)) {
    if (!line || line.startsWith(":")) continue;
    const separator = line.indexOf(":");
    const field = separator < 0 ? line : line.slice(0, separator);
    let value = separator < 0 ? "" : line.slice(separator + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "data") data.push(value);
    if (field === "id" && !value.includes("\0")) eventID = value;
  }
  if (data.length === 0) return null;
  return { data: data.join("\n"), eventID };
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

import { useEffect, useRef, useState } from "react";
import { resolveBackendUrl } from "../../lib/backend";
import { isPlainRecord } from "../../lib/utils";
import type { RuntimeEvent } from "../../types";
import { runtimeEventTypes } from "./constants";

export type RuntimeEventStreamStatus = "connecting" | "connected" | "reconnecting" | "closed";

export function parseRuntimeEventFrame(data: string): RuntimeEvent | null {
  try {
    const parsed = JSON.parse(data) as unknown;
    if (
      !isPlainRecord(parsed) ||
      typeof parsed.id !== "string" ||
      typeof parsed.run_id !== "string" ||
      typeof parsed.type !== "string" ||
      typeof parsed.timestamp !== "string"
    ) {
      return null;
    }
    return parsed as unknown as RuntimeEvent;
  } catch {
    return null;
  }
}

export function useRuntimeEventStream(onEvent: (event: RuntimeEvent) => void): RuntimeEventStreamStatus {
  const [status, setStatus] = useState<RuntimeEventStreamStatus>("connecting");
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  useEffect(() => {
    const streamURL = resolveBackendUrl("/events/stream");
    let source: EventSource | null = null;
    let reconnectTimer: number | null = null;
    let closed = false;

    const onMessage = (message: MessageEvent<string>) => {
      const event = parseRuntimeEventFrame(message.data);
      if (event) onEventRef.current(event);
    };

    const closeSource = (target: EventSource) => {
      target.onopen = null;
      target.onmessage = null;
      target.onerror = null;
      for (const eventType of runtimeEventTypes) {
        target.removeEventListener(eventType, onMessage as EventListener);
      }
      target.close();
      if (source === target) source = null;
    };

    const connect = () => {
      if (closed) return;
      setStatus((current) => current === "reconnecting" ? "reconnecting" : "connecting");
      const nextSource = new EventSource(streamURL);
      source = nextSource;
      nextSource.onopen = () => {
        if (!closed && source === nextSource) setStatus("connected");
      };
      nextSource.onmessage = onMessage;
      for (const eventType of runtimeEventTypes) {
        nextSource.addEventListener(eventType, onMessage as EventListener);
      }
      nextSource.onerror = () => {
        if (closed || source !== nextSource) return;
        setStatus("reconnecting");
        closeSource(nextSource);
        if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
        reconnectTimer = window.setTimeout(() => {
          reconnectTimer = null;
          connect();
        }, 1500);
      };
    };

    connect();
    return () => {
      closed = true;
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
      if (source) closeSource(source);
    };
  }, []);

  return status;
}

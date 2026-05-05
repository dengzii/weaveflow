import { useEffect, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import type { LiveState, ReplayItem, RunDetail, RunRecord } from "../replay/types";
import { api, buildUrl } from "../replay/api";

export type PageMode = "history" | "live";
export type LiveSocketState = "idle" | "connecting" | "connected" | "disconnected";

type LiveMsg =
  | {
      type: "snapshot";
      run_id: string;
      source_name: string;
      graph_ref: string;
      started_at: string;
      graph: unknown;
      items: ReplayItem[];
    }
  | { type: "item"; item: ReplayItem }
  | { type: "item.update"; item_idx: number; item: ReplayItem }
  | { type: "done"; run_id?: string }
  | { type: "idle" };

export function useLiveMode({
  cacheDir,
  setDetail,
  setReplayIndex,
  setStatus,
  onEnterLive,
  onExitLive,
}: {
  cacheDir: string;
  setDetail: Dispatch<SetStateAction<RunDetail | null>>;
  setReplayIndex: (i: number) => void;
  setStatus: Dispatch<SetStateAction<{ message: string; summary: string }>>;
  onEnterLive: () => void;
  onExitLive: () => Promise<void>;
}) {
  const [mode, setMode] = useState<PageMode>("history");
  const [liveState, setLiveState] = useState<LiveState | null>(null);
  const [liveSocketState, setLiveSocketState] = useState<LiveSocketState>("idle");
  const [liveDuration, setLiveDuration] = useState(0);

  const liveWsRef = useRef<WebSocket | null>(null);
  const liveStartedAtRef = useRef(0);
  const liveEventsListRef = useRef<HTMLDivElement | null>(null);
  const modeRef = useRef<PageMode>("history");
  const liveStateRef = useRef<LiveState | null>(null);
  const liveSocketStateRef = useRef<LiveSocketState>("idle");
  const onEnterLiveRef = useRef(onEnterLive);
  const onExitLiveRef = useRef(onExitLive);

  useEffect(() => { modeRef.current = mode; }, [mode]);
  useEffect(() => { liveStateRef.current = liveState; }, [liveState]);
  useEffect(() => { liveSocketStateRef.current = liveSocketState; }, [liveSocketState]);
  useEffect(() => { onEnterLiveRef.current = onEnterLive; }, [onEnterLive]);
  useEffect(() => { onExitLiveRef.current = onExitLive; }, [onExitLive]);

  function writeLiveState(next: LiveState | null) {
    liveStateRef.current = next;
    setLiveState(next);
  }

  function writeLiveSocketState(next: LiveSocketState) {
    liveSocketStateRef.current = next;
    setLiveSocketState(next);
  }

  function applyLiveSnapshot(snapshot: LiveState) {
    const normalized: LiveState = {
      running: Boolean(snapshot.running),
      run_id: snapshot.run_id || "",
      source_name: snapshot.source_name || "",
      graph_ref: snapshot.graph_ref || "",
      started_at: snapshot.started_at,
      graph: snapshot.graph,
      items: snapshot.items ?? [],
    };
    writeLiveState(normalized);
    if (normalized.running && normalized.started_at) {
      liveStartedAtRef.current = new Date(normalized.started_at).getTime();
    } else {
      liveStartedAtRef.current = 0;
      setLiveDuration(0);
    }
    setDetail(buildLiveDetailFromState(normalized));
    setReplayIndex(normalized.items.length ? normalized.items.length - 1 : 0);
    setStatus(buildLiveStatus(normalized, liveSocketStateRef.current));
  }

  function appendLiveItem(item: ReplayItem) {
    const previous = liveStateRef.current;
    const startedAt = previous?.started_at || new Date().toISOString();
    const next: LiveState = {
      running: true,
      run_id: previous?.run_id || item.event.run_id || "",
      source_name: previous?.source_name || "Neo Agent",
      graph_ref: previous?.graph_ref || "live",
      started_at: startedAt,
      graph: previous?.graph,
      items: [...(previous?.items ?? []), item],
    };
    writeLiveState(next);
    if (!liveStartedAtRef.current) {
      liveStartedAtRef.current = startedAt ? new Date(startedAt).getTime() : Date.now();
    }
    setDetail((current) => {
      if (!current) {
        return buildLiveDetail(
          next.run_id,
          next.source_name,
          next.graph_ref,
          next.graph,
          [item],
          true,
          next.started_at
        );
      }
      const replay = [...current.replay, item];
      return {
        ...current,
        summary: { ...current.summary, event_count: replay.length },
        run: {
          ...current.run,
          run_id: next.run_id || current.run.run_id,
          status: "running",
          updated_at: item.timestamp || new Date().toISOString(),
        },
        replay,
      };
    });
    setReplayIndex(next.items.length ? next.items.length - 1 : 0);
    setStatus(buildLiveStatus(next, liveSocketStateRef.current));
  }

  function updateLiveItem(idx: number, item: ReplayItem) {
    const previous = liveStateRef.current;
    if (!previous) return;
    const items = previous.items ? [...previous.items] : [];
    if (idx < 0 || idx >= items.length) return;
    items[idx] = item;
    const next: LiveState = { ...previous, items };
    writeLiveState(next);
    setDetail((current) => {
      if (!current) return current;
      const replay = [...current.replay];
      if (idx >= 0 && idx < replay.length) replay[idx] = item;
      return { ...current, replay };
    });
    setStatus(buildLiveStatus(next, liveSocketStateRef.current));
  }

  function enterLiveMode() {
    onEnterLiveRef.current();
    setMode("live");
    writeLiveState(null);
    writeLiveSocketState("connecting");
    liveStartedAtRef.current = 0;
    setLiveDuration(0);
    setStatus({ message: "Connecting live stream...", summary: "" });
  }

  async function exitLiveMode() {
    setMode("history");
    writeLiveState(null);
    writeLiveSocketState("idle");
    await onExitLiveRef.current();
  }

  useEffect(() => {
    if (mode !== "live") {
      liveWsRef.current?.close();
      liveWsRef.current = null;
      liveStartedAtRef.current = 0;
      setLiveDuration(0);
      return;
    }

    let disposed = false;
    let closing = false;
    let streamHydrated = false;

    writeLiveSocketState("connecting");

    void api<LiveState>(buildUrl("/api/live", cacheDir))
      .then((snapshot) => {
        if (disposed || streamHydrated) return;
        applyLiveSnapshot(snapshot);
      })
      .catch((error) => {
        if (disposed) return;
        setStatus((current) => ({
          message: `Live snapshot failed: ${(error as Error).message}`,
          summary: current.summary,
        }));
      });

    const wsProto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${wsProto}//${window.location.host}/api/ws`);
    liveWsRef.current = ws;

    ws.onopen = () => {
      if (disposed) return;
      writeLiveSocketState("connected");
      setStatus(buildLiveStatus(liveStateRef.current, "connected"));
    };

    ws.onmessage = (e: MessageEvent<string>) => {
      if (disposed) return;
      const msg = JSON.parse(e.data) as LiveMsg;
      if (msg.type === "idle") {
        liveStartedAtRef.current = 0;
        setLiveDuration(0);
        setStatus(buildLiveStatus(liveStateRef.current, liveSocketStateRef.current));
        return;
      }
      if (msg.type === "snapshot") {
        streamHydrated = true;
        applyLiveSnapshot({
          running: true,
          run_id: msg.run_id || "",
          source_name: msg.source_name || "",
          graph_ref: msg.graph_ref || "",
          started_at: msg.started_at,
          graph: msg.graph,
          items: msg.items ?? [],
        });
        return;
      }
      if (msg.type === "item") {
        streamHydrated = true;
        appendLiveItem(msg.item);
        return;
      }
      if (msg.type === "item.update") {
        streamHydrated = true;
        updateLiveItem(msg.item_idx, msg.item);
        return;
      }
      if (msg.type === "done") {
        liveStartedAtRef.current = 0;
        setLiveDuration(0);
        const previous = liveStateRef.current;
        const next = previous ? { ...previous, running: false } : previous;
        writeLiveState(next);
        setStatus(buildLiveStatus(next, liveSocketStateRef.current));
      }
    };

    ws.onerror = () => {
      if (disposed) return;
      writeLiveSocketState("disconnected");
      liveStartedAtRef.current = 0;
      setLiveDuration(0);
      setStatus(buildLiveStatus(liveStateRef.current, "disconnected"));
    };

    ws.onclose = () => {
      if (disposed || closing) return;
      writeLiveSocketState("disconnected");
      liveStartedAtRef.current = 0;
      setLiveDuration(0);
      setStatus(buildLiveStatus(liveStateRef.current, "disconnected"));
    };

    return () => {
      disposed = true;
      closing = true;
      ws.close();
      liveWsRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, cacheDir]);

  useEffect(() => {
    if (mode !== "live") return;
    const timer = window.setInterval(() => {
      if (liveStartedAtRef.current) {
        setLiveDuration(Date.now() - liveStartedAtRef.current);
      }
    }, 500);
    return () => clearInterval(timer);
  }, [mode]);

  return {
    mode,
    modeRef,
    isLiveMode: mode === "live",
    liveState,
    liveSocketState,
    liveDuration,
    liveEventsListRef,
    enterLiveMode,
    exitLiveMode,
    liveBadge: liveBadgeLabel(liveState, liveSocketState),
  };
}

export function buildLiveStatus(
  snapshot: LiveState | null,
  socketState: LiveSocketState
): { message: string; summary: string } {
  const summary = liveSummaryText(snapshot);
  if (socketState === "disconnected") {
    return { message: "Live stream disconnected.", summary };
  }
  if (socketState === "connecting") {
    return { message: "Connecting live stream...", summary };
  }
  if (snapshot?.running) {
    return { message: "Streaming live graph events.", summary };
  }
  if (summary) {
    return { message: "Live graph ready. Waiting for the next request.", summary };
  }
  return { message: "Waiting for the first live graph snapshot.", summary: "" };
}

export function liveBadgeLabel(snapshot: LiveState | null, socketState: LiveSocketState): string {
  if (socketState === "disconnected") return "Offline";
  if (socketState === "connecting") return "Connecting";
  if (snapshot?.running) return "Running";
  if (snapshot?.graph || snapshot?.graph_ref || snapshot?.source_name) return "Waiting";
  return "Listening";
}

export function visibleLiveStatus(message: string, socketState: LiveSocketState): string {
  const text = message.trim();
  if (!text) return "";
  if (socketState === "disconnected") return text;
  if (text.toLowerCase().includes("failed")) return text;
  return "";
}

function liveSummaryText(snapshot: LiveState | null): string {
  if (!snapshot) return "";
  return [snapshot.source_name, snapshot.graph_ref].filter(Boolean).join(" | ");
}

function buildLiveDetailFromState(snapshot: LiveState): RunDetail | null {
  const hasGraph = Boolean(snapshot.graph || snapshot.graph_ref || snapshot.source_name);
  if (!hasGraph && snapshot.items.length === 0 && !snapshot.running) {
    return null;
  }
  return buildLiveDetail(
    snapshot.run_id,
    snapshot.source_name,
    snapshot.graph_ref,
    snapshot.graph,
    snapshot.items,
    snapshot.running,
    snapshot.started_at
  );
}

function buildLiveDetail(
  runId: string,
  sourceName: string,
  graphRef: string,
  graph: unknown,
  items: ReplayItem[],
  running: boolean,
  startedAt?: string
): RunDetail {
  const now = new Date().toISOString();
  const started = startedAt || now;
  const name = sourceName || "Neo Agent";
  const ref = graphRef || "live";
  const run: RunRecord = {
    run_id: runId || "live",
    graph_id: ref,
    graph_version: "",
    status: running ? "running" : "idle",
    entry_node_id: "",
    current_node_id: "",
    last_step_id: "",
    error_message: "",
    started_at: started,
    updated_at: now,
  };
  return {
    summary: {
      source_id: "live",
      source_name: name,
      cache_root: "",
      instance_id: "",
      graph_ref: ref,
      graph_version: "",
      run,
      duration_ms: 0,
      step_count: 0,
      event_count: items.length,
      checkpoint_count: 0,
      artifact_count: 0,
    },
    source: {
      id: "live",
      name,
      root: "",
      instance_id: "",
      graph_ref: ref,
      graph_version: "",
      instance: null,
      graph,
      warnings: [],
    },
    run,
    replay: items,
    steps: [],
    events: [],
    checkpoints: [],
    artifacts: [],
  };
}

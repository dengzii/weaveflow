import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { runtimeEventTypes } from "./constants";
import {
  RuntimeEventStreamClient,
  SSEFrameDecoder,
  parseRuntimeEventFrame,
  type RuntimeEventStreamState,
} from "./useRuntimeEventStream";

describe("runtime event stream model", () => {
  test("parses runtime, gap, unknown, and malformed frames", () => {
    expect(parseRuntimeEventFrame('{"id":"event-1","graph_id":"graph-1","run_id":"run-1","type":"run.started","timestamp":"2026-07-31T00:00:00Z"}')).toEqual({
      id: "event-1",
      graph_id: "graph-1",
      run_id: "run-1",
      type: "run.started",
      timestamp: "2026-07-31T00:00:00Z",
    });
    expect(parseRuntimeEventFrame('{"id":"event-2","graph_id":"graph-1","run_id":"run-1","type":"extension.progress","timestamp":"2026-07-31T00:00:01Z"}')).toMatchObject({
      id: "event-2",
      type: "extension.progress",
    });
    expect(parseRuntimeEventFrame('{"type":"stream.gap","graph_id":"graph-1","requested_cursor":"old","resume_cursor":"new","reason":"cursor_expired","recoverable_events":"persistent_only"}')).toMatchObject({
      type: "stream.gap",
      reason: "cursor_expired",
      resume_cursor: "new",
    });
    expect(parseRuntimeEventFrame('{"id":"event-1"}')).toBeNull();
    expect(parseRuntimeEventFrame("[]")).toBeNull();
    expect(parseRuntimeEventFrame("not-json")).toBeNull();
  });

  test("decodes fragmented unnamed SSE data frames", () => {
    const decoder = new SSEFrameDecoder();
    expect(decoder.push(": heartbeat\n\nid: event-1\nda")).toEqual([]);
    expect(decoder.push('ta: {"type":"extension.progress"}\n\n')).toEqual([
      { eventID: "event-1", data: '{"type":"extension.progress"}' },
    ]);
  });

  test("invokes the default fetch with the global receiver", async () => {
    const originalFetch = globalThis.fetch;
    let receiver: unknown;
    globalThis.fetch = function(this: unknown) {
      receiver = this;
      return Promise.resolve(new Response(null, { status: 404 }));
    } as typeof fetch;
    try {
      const client = new RuntimeEventStreamClient({
        onEvent: () => undefined,
        onGap: () => undefined,
        onState: () => undefined,
      });
      client.start("graph-1");
      await flushAsyncWork();
      expect(receiver).toBe(globalThis);
      client.stop(false);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  test("receives gap and unknown events while advancing the cursor", async () => {
    const events: string[] = [];
    const gaps: string[] = [];
    const states: RuntimeEventStreamState[] = [];
    const timers: Array<() => void> = [];
    const stream = [
      'data: {"type":"stream.gap","graph_id":"graph-1","requested_cursor":"old","resume_cursor":"resume","reason":"cursor_expired","recoverable_events":"persistent_only"}\n\n',
      'id: extension-1\ndata: {"id":"extension-1","graph_id":"graph-1","run_id":"run-1","type":"extension.progress","timestamp":"2026-08-11T00:00:00Z"}\n\n',
    ].join("");
    const client = new RuntimeEventStreamClient({
      onEvent: (event) => events.push(event.type),
      onGap: (gap) => gaps.push(gap.reason),
      onState: (state) => states.push(state),
      fetcher: async () => streamResponse(stream),
      setTimer: (callback) => {
        timers.push(callback);
        return timers.length;
      },
      clearTimer: () => undefined,
    });

    client.start("graph-1");
    await flushAsyncWork();

    expect(gaps).toEqual(["cursor_expired"]);
    expect(events).toEqual(["extension.progress"]);
    expect(states.some((state) => state.status === "gap")).toBe(true);
    expect(states.at(-1)?.diagnostics.lastEventID).toBe("extension-1");
    expect(timers.length).toBeGreaterThanOrEqual(1);
    timers[0]?.();
    expect(states.at(-1)?.diagnostics).toMatchObject({
      receivedEventsPerSecond: 1,
      discardedFramesPerSecond: 0,
    });
    client.stop(false);
  });

  test("stops on 4xx and 5xx without automatic retries", async () => {
    const clientStates: RuntimeEventStreamState[] = [];
    const clientTimers: Array<() => void> = [];
    const client = new RuntimeEventStreamClient({
      onEvent: () => undefined,
      onGap: () => undefined,
      onState: (state) => clientStates.push(state),
      fetcher: async () => new Response(null, { status: 404 }),
      setTimer: (callback) => {
        clientTimers.push(callback);
        return clientTimers.length;
      },
      clearTimer: () => undefined,
    });
    client.start("missing");
    await flushAsyncWork();
    expect(clientStates.at(-1)?.status).toBe("failed");
    expect(clientStates.at(-1)?.diagnostics.lastErrorKind).toBe("client_error");
    expect(clientTimers).toHaveLength(0);
    client.stop(false);

    const serverStates: RuntimeEventStreamState[] = [];
    const serverTimers: Array<() => void> = [];
    const server = new RuntimeEventStreamClient({
      onEvent: () => undefined,
      onGap: () => undefined,
      onState: (state) => serverStates.push(state),
      fetcher: async () => new Response(null, { status: 503 }),
      setTimer: (callback) => {
        serverTimers.push(callback);
        return serverTimers.length;
      },
      clearTimer: () => undefined,
    });
    server.start("graph-1");
    await flushAsyncWork();
    expect(serverStates.at(-1)?.status).toBe("failed");
    expect(serverStates.at(-1)?.diagnostics).toMatchObject({
      lastErrorKind: "server_error",
    });
    expect(serverTimers).toHaveLength(0);
    server.stop(false);
  });

  test("retries only when requested", async () => {
    let requests = 0;
    const states: RuntimeEventStreamState[] = [];
    const timers: Array<() => void> = [];
    const clearedTimers: number[] = [];
    const client = new RuntimeEventStreamClient({
      onEvent: () => undefined,
      onGap: () => undefined,
      onState: (state) => states.push(state),
      fetcher: async () => {
        requests += 1;
        return new Response(null, { status: 503 });
      },
      setTimer: (callback) => {
        timers.push(callback);
        return timers.length;
      },
      clearTimer: (timer) => clearedTimers.push(timer),
    });

    client.start("graph-1");
    await flushAsyncWork();
    expect(requests).toBe(1);
    expect(states.at(-1)?.status).toBe("failed");

    client.reconnectNow();
    expect(states.at(-1)).toMatchObject({
      status: "connecting",
      diagnostics: { lastError: "" },
    });
    await flushAsyncWork();

    expect(requests).toBe(2);
    expect(states.at(-1)?.status).toBe("failed");
    expect(clearedTimers).toHaveLength(0);
    client.stop(false);
  });

  test("ignores a superseded connection response", async () => {
    let resolveFirst: ((response: Response) => void) | undefined;
    let requests = 0;
    const events: string[] = [];
    const states: RuntimeEventStreamState[] = [];
    const client = new RuntimeEventStreamClient({
      onEvent: (event) => events.push(event.id),
      onGap: () => undefined,
      onState: (state) => states.push(state),
      fetcher: async () => {
        requests += 1;
        if (requests === 1) {
          return await new Promise<Response>((resolveResponse) => {
            resolveFirst = resolveResponse;
          });
        }
        return new Response(null, { status: 404 });
      },
    });

    client.start("graph-a");
    client.start("graph-b");
    resolveFirst?.(streamResponse('id: stale\ndata: {"id":"stale","graph_id":"graph-a","run_id":"run","type":"run.started","timestamp":"2026-08-11T00:00:00Z"}\n\n'));
    await flushAsyncWork();

    expect(events).toEqual([]);
    expect(states.at(-1)?.status).toBe("failed");
    client.stop(false);
  });

  test("keeps the Go and WebUI runtime event lists in parity", () => {
    const goSource = readFileSync(
      resolve(import.meta.dir, "../../../../runtime/runner_types.go"),
      "utf8"
    );
    const goEventTypes = [...goSource.matchAll(/Event\w+\s+EventType\s*=\s*"([^"]+)"/g)]
      .map((match) => match[1])
      .sort();
    expect([...runtimeEventTypes].sort()).toEqual(goEventTypes);
  });
});

function streamResponse(data: string): Response {
  const bytes = new TextEncoder().encode(data);
  return new Response(new ReadableStream({
    start(controller) {
      const midpoint = Math.floor(bytes.length / 2);
      controller.enqueue(bytes.slice(0, midpoint));
      controller.enqueue(bytes.slice(midpoint));
      controller.close();
    },
  }), {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  });
}

async function flushAsyncWork(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
  await new Promise((resolvePromise) => setTimeout(resolvePromise, 0));
}

import type { RuntimeEvent } from "../types";

const runtimeEventBatchName = "weaveflow:runtime-event-batch";

export function emitRuntimeEvents(events: RuntimeEvent[]) {
  if (events.length === 0) return;
  window.dispatchEvent(new CustomEvent<RuntimeEvent[]>(runtimeEventBatchName, { detail: events }));
}

export function subscribeRuntimeEvents(handler: (event: RuntimeEvent) => void): () => void {
  const batchListener = (event: Event) => {
    for (const runtimeEvent of (event as CustomEvent<RuntimeEvent[]>).detail) {
      handler(runtimeEvent);
    }
  };
  window.addEventListener(runtimeEventBatchName, batchListener);
  return () => {
    window.removeEventListener(runtimeEventBatchName, batchListener);
  };
}

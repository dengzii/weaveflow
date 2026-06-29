import type { RuntimeEvent } from "../types";

const runtimeEventName = "weaveflow:runtime-event";

export function emitRuntimeEvent(event: RuntimeEvent) {
  window.dispatchEvent(new CustomEvent<RuntimeEvent>(runtimeEventName, { detail: event }));
}

export function subscribeRuntimeEvents(handler: (event: RuntimeEvent) => void): () => void {
  const listener = (event: Event) => {
    handler((event as CustomEvent<RuntimeEvent>).detail);
  };
  window.addEventListener(runtimeEventName, listener);
  return () => window.removeEventListener(runtimeEventName, listener);
}

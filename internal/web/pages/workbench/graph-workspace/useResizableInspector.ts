import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";

const inspectorWidthStorageKey = "weaveflow.graphWorkspace.inspectorWidth";
const defaultInspectorWidth = 380;
const minInspectorWidth = 320;
const maxInspectorWidth = 720;
const minCanvasWidth = 360;
const separatorWidth = 6;

export function clampInspectorWidth(width: number, containerWidth?: number): number {
  const availableWidth = typeof containerWidth === "number" && Number.isFinite(containerWidth)
    ? containerWidth
    : defaultInspectorWidth + minCanvasWidth + separatorWidth;
  const maxByContainer = Math.max(minInspectorWidth, availableWidth - minCanvasWidth - separatorWidth);
  const upperBound = Math.max(minInspectorWidth, Math.min(maxInspectorWidth, maxByContainer));
  return Math.max(minInspectorWidth, Math.min(upperBound, Math.round(width)));
}

export function useResizableInspector() {
  const workspaceRef = useRef<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(readStoredInspectorWidth);
  const dragCleanupRef = useRef<(() => void) | null>(null);

  const stopResize = useCallback(() => {
    const cleanup = dragCleanupRef.current;
    dragCleanupRef.current = null;
    cleanup?.();
  }, []);

  const startResize = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    stopResize();
    const workspaceRight = workspaceRef.current?.getBoundingClientRect().right ?? window.innerWidth;
    const workspaceWidth = workspaceRef.current?.clientWidth;
    const previousCursor = document.body.style.cursor;
    const previousUserSelect = document.body.style.userSelect;
    const onMove = (moveEvent: PointerEvent) => {
      const nextWidth = clampInspectorWidth(workspaceRight - moveEvent.clientX, workspaceWidth);
      setWidth(nextWidth);
      writeStoredInspectorWidth(nextWidth);
    };
    const onStop = () => stopResize();
    dragCleanupRef.current = () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onStop);
      window.removeEventListener("pointercancel", onStop);
      document.body.style.cursor = previousCursor;
      document.body.style.userSelect = previousUserSelect;
    };
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onStop);
    window.addEventListener("pointercancel", onStop);
  }, [stopResize]);

  useEffect(() => {
    const clampWidth = () => {
      setWidth((current) => {
        const next = clampInspectorWidth(current, workspaceRef.current?.clientWidth);
        if (next !== current) writeStoredInspectorWidth(next);
        return next;
      });
    };
    window.addEventListener("resize", clampWidth);
    return () => window.removeEventListener("resize", clampWidth);
  }, []);

  useEffect(() => stopResize, [stopResize]);

  return { workspaceRef, width, startResize };
}

function readStoredInspectorWidth(): number {
  if (typeof window === "undefined") return defaultInspectorWidth;
  try {
    const raw = window.localStorage.getItem(inspectorWidthStorageKey);
    const parsed = raw ? Number(raw) : NaN;
    return clampInspectorWidth(Number.isFinite(parsed) ? parsed : defaultInspectorWidth, window.innerWidth);
  } catch {
    return defaultInspectorWidth;
  }
}

function writeStoredInspectorWidth(width: number): void {
  if (typeof window === "undefined" || !Number.isFinite(width)) return;
  try {
    window.localStorage.setItem(inspectorWidthStorageKey, String(Math.round(width)));
  } catch {
    // Inspector width persistence is best effort.
  }
}

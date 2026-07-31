import type { Node, Viewport } from "@xyflow/react";
import {
  graphNodeHeight,
  graphNodeWidth,
  type FlowNodeData,
} from "./graphCanvasModel";

export const minGraphCanvasZoom = 0.2;
export const maxGraphCanvasZoom = 2;

const viewportStoragePrefix = "weaveflow.workbench.graphCanvas.viewport.";

interface StoredCanvasViewport {
  x: number;
  y: number;
  zoom: number;
}

interface D3ZoomTransform {
  x: number;
  y: number;
  k: number;
}

interface D3ZoomElement extends Element {
  __zoom?: D3ZoomTransform;
}

export function fitNodesToViewport(
  nodes: Node<FlowNodeData>[],
  viewportElement: HTMLDivElement | null,
  applyViewport: (viewport: Viewport) => void,
  padding = 0.2
): boolean {
  const rect = viewportElement?.getBoundingClientRect();
  if (!rect || rect.width <= 0 || rect.height <= 0 || nodes.length === 0) return false;

  const bounds = nodes.reduce(
    (current, node) => {
      const dimensions = flowNodeDimensions(node);
      return {
        minX: Math.min(current.minX, node.position.x),
        minY: Math.min(current.minY, node.position.y),
        maxX: Math.max(current.maxX, node.position.x + dimensions.width),
        maxY: Math.max(current.maxY, node.position.y + dimensions.height),
      };
    },
    { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity }
  );
  if (!Number.isFinite(bounds.minX) || !Number.isFinite(bounds.minY)) return false;

  const width = Math.max(bounds.maxX - bounds.minX, graphNodeWidth);
  const height = Math.max(bounds.maxY - bounds.minY, graphNodeHeight);
  const zoom = Math.max(
    minGraphCanvasZoom,
    Math.min(
      maxGraphCanvasZoom,
      Math.min(rect.width / (width * (1 + padding * 2)), rect.height / (height * (1 + padding * 2)))
    )
  );
  const centerX = bounds.minX + width / 2;
  const centerY = bounds.minY + height / 2;
  applyViewport({
    x: rect.width / 2 - centerX * zoom,
    y: rect.height / 2 - centerY * zoom,
    zoom,
  });
  return true;
}

export function sameViewport(first: Viewport, second: Viewport): boolean {
  return (
    Math.abs(first.x - second.x) < 0.01 &&
    Math.abs(first.y - second.y) < 0.01 &&
    Math.abs(first.zoom - second.zoom) < 0.0001
  );
}

export function normalizeViewportStorageKey(key?: string): string {
  const trimmed = key?.trim();
  return trimmed ? `${viewportStoragePrefix}${trimmed}` : "";
}

export function hasStoredGraphCanvasViewport(key?: string): boolean {
  return Boolean(readStoredCanvasViewport(normalizeViewportStorageKey(key)));
}

export function readStoredCanvasViewport(key: string): Viewport | null {
  if (typeof window === "undefined" || !key) return null;
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as StoredCanvasViewport;
    if (!isFiniteNumber(parsed.x) || !isFiniteNumber(parsed.y) || !isFiniteNumber(parsed.zoom)) return null;
    return {
      x: parsed.x,
      y: parsed.y,
      zoom: Math.max(minGraphCanvasZoom, Math.min(maxGraphCanvasZoom, parsed.zoom)),
    };
  } catch {
    return null;
  }
}

export function writeStoredCanvasViewport(key: string | undefined, viewport: Viewport): void {
  const storageKey = normalizeViewportStorageKey(key);
  if (typeof window === "undefined" || !storageKey) return;
  if (!isFiniteNumber(viewport.x) || !isFiniteNumber(viewport.y) || !isFiniteNumber(viewport.zoom)) return;
  try {
    window.localStorage.setItem(
      storageKey,
      JSON.stringify({
        x: viewport.x,
        y: viewport.y,
        zoom: Math.max(minGraphCanvasZoom, Math.min(maxGraphCanvasZoom, viewport.zoom)),
      })
    );
  } catch {
    // Viewport persistence is best effort.
  }
}

export function syncRendererZoomState(viewportElement: HTMLElement | null, viewport: Viewport): void {
  const root = viewportElement ?? document.body;
  const renderers = [
    ...root.querySelectorAll<D3ZoomElement>(".react-flow__renderer"),
    ...root.ownerDocument.querySelectorAll<D3ZoomElement>(".react-flow__renderer"),
  ];

  for (const renderer of new Set(renderers)) {
    const current = renderer.__zoom;
    if (!current) continue;

    // React Flow renders from store.transform while d3-zoom keeps separate gesture state.
    const ZoomTransform = current.constructor as new (k: number, x: number, y: number) => D3ZoomTransform;
    renderer.__zoom = new ZoomTransform(viewport.zoom, viewport.x, viewport.y);
  }
}

function flowNodeDimensions(node: Node<FlowNodeData>): { width: number; height: number } {
  const dataWidth = typeof node.data.width === "number" ? node.data.width : 0;
  const dataHeight = typeof node.data.height === "number" ? node.data.height : 0;
  return {
    width: dataWidth || node.measured?.width || node.width || graphNodeWidth,
    height: dataHeight || node.measured?.height || node.height || graphNodeHeight,
  };
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

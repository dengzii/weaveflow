import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import type { NodePosition } from "../../../lib/graphEditor";
import type { GraphNodeSpec, NodeTypeSchema } from "../../../types";
import { virtualNodeTypes } from "./constants";
import type { CanvasContextMenu as CanvasContextMenuState, VirtualNodeKind } from "./types";

interface CanvasContextMenuProps {
  boundaryRef: RefObject<HTMLElement | null>;
  contextMenu: CanvasContextMenuState;
  paletteNodeTypes: NodeTypeSchema[];
  onAddNode: (nodeType: NodeTypeSchema, position?: NodePosition) => void;
  onAddVirtualNode: (kind: VirtualNodeKind, position?: NodePosition) => void;
  onClose: () => void;
  onDeleteEdge: (edgeId: string) => void;
  onDeleteLoop: (loopId: string) => void;
  onDeleteNode: (nodeId: string) => void;
}

interface MenuLayout {
  left: number;
  top: number;
  maxHeight: number;
  maxWidth: number;
}

export function CanvasContextMenu({
  boundaryRef,
  contextMenu,
  paletteNodeTypes,
  onAddNode,
  onAddVirtualNode,
  onClose,
  onDeleteEdge,
  onDeleteLoop,
  onDeleteNode,
}: CanvasContextMenuProps) {
  const menuRef = useRef<HTMLDivElement | null>(null);
  const [layout, setLayout] = useState<MenuLayout | null>(null);

  useLayoutEffect(() => {
    const menu = menuRef.current;
    if (!menu) return;

    const updateLayout = () => {
      const boundary = boundaryRef.current?.getBoundingClientRect();
      const margin = 8;
      const leftBound = Math.max(margin, (boundary?.left ?? 0) + margin);
      const topBound = Math.max(margin, (boundary?.top ?? 0) + margin);
      const rightBound = Math.min(window.innerWidth - margin, (boundary?.right ?? window.innerWidth) - margin);
      const bottomBound = Math.min(window.innerHeight - margin, (boundary?.bottom ?? window.innerHeight) - margin);
      const maxWidth = Math.max(0, rightBound - leftBound);
      const maxHeight = Math.max(0, bottomBound - topBound);
      const menuWidth = Math.min(menu.getBoundingClientRect().width, maxWidth);
      const menuHeight = Math.min(menu.scrollHeight, maxHeight);
      const preferredLeft = contextMenu.screen.x;
      const preferredTop = contextMenu.screen.y;
      const left = clampMenuCoordinate(
        preferredLeft + menuWidth > rightBound ? preferredLeft - menuWidth : preferredLeft,
        leftBound,
        rightBound - menuWidth
      );
      const top = clampMenuCoordinate(
        preferredTop + menuHeight > bottomBound ? preferredTop - menuHeight : preferredTop,
        topBound,
        bottomBound - menuHeight
      );
      const nextLayout = { left, top, maxHeight, maxWidth };
      setLayout((current) =>
        current &&
        current.left === nextLayout.left &&
        current.top === nextLayout.top &&
        current.maxHeight === nextLayout.maxHeight &&
        current.maxWidth === nextLayout.maxWidth
          ? current
          : nextLayout
      );
    };

    updateLayout();
    window.addEventListener("resize", updateLayout);
    const resizeObserver = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(updateLayout);
    resizeObserver?.observe(menu);
    if (boundaryRef.current) resizeObserver?.observe(boundaryRef.current);
    return () => {
      window.removeEventListener("resize", updateLayout);
      resizeObserver?.disconnect();
    };
  }, [boundaryRef, contextMenu]);

  return (
    <div
      ref={menuRef}
      className="fixed z-50 max-h-[calc(100vh-1rem)] w-64 overflow-y-auto rounded-md border border-border bg-panel shadow-lg"
      style={{
        left: layout?.left ?? contextMenu.screen.x,
        top: layout?.top ?? contextMenu.screen.y,
        maxHeight: layout ? `${layout.maxHeight}px` : undefined,
        maxWidth: layout ? `${layout.maxWidth}px` : undefined,
        visibility: layout ? "visible" : "hidden",
      }}
      onClick={(event) => event.stopPropagation()}
      onContextMenu={(event) => event.preventDefault()}
    >
      {contextMenu.kind === "pane" ? (
        <div>
          <ContextMenuTitle>Create Node</ContextMenuTitle>
          {virtualNodeTypes.map((nodeType) => (
            <CreateNodeItem
              key={nodeType.type}
              nodeType={nodeType}
              onClick={() => onAddVirtualNode(nodeType.type as VirtualNodeKind, contextMenu.position)}
            />
          ))}
          {paletteNodeTypes.map((nodeType) => (
            <CreateNodeItem
              key={nodeType.type}
              nodeType={nodeType}
              onClick={() => onAddNode(nodeType, contextMenu.position)}
            />
          ))}
        </div>
      ) : null}

      {contextMenu.kind === "node" ? (
        <div>
          <ContextMenuTitle>Node</ContextMenuTitle>
          <ContextMenuAction label="Edit" onClick={onClose} />
          <ContextMenuAction
            label="Delete"
            tone="destructive"
            onClick={() => onDeleteNode(contextMenu.nodeId)}
          />
        </div>
      ) : null}

      {contextMenu.kind === "edge" ? (
        <div>
          <ContextMenuTitle>Edge</ContextMenuTitle>
          <ContextMenuAction label="Edit" onClick={onClose} />
          <ContextMenuAction
            label="Delete"
            tone="destructive"
            onClick={() => onDeleteEdge(contextMenu.edgeId)}
          />
        </div>
      ) : null}

      {contextMenu.kind === "loop" ? (
        <div>
          <ContextMenuTitle>Loop</ContextMenuTitle>
          <ContextMenuAction label="Edit" onClick={onClose} />
          <ContextMenuAction
            label="Delete"
            tone="destructive"
            onClick={() => onDeleteLoop(contextMenu.loopId)}
          />
        </div>
      ) : null}
    </div>
  );
}

function ContextMenuTitle({ children }: { children: string }) {
  return <div className="border-b border-border px-3 py-2 text-xs font-semibold uppercase text-muted-foreground">{children}</div>;
}

function CreateNodeItem({
  nodeType,
  onClick,
}: {
  nodeType: Pick<GraphNodeSpec, "type" | "name"> & Pick<NodeTypeSchema, "title">;
  onClick: () => void;
}) {
  return (
    <button className="block w-full min-w-0 px-3 py-2 text-left text-sm font-medium hover:bg-accent" onClick={onClick}>
      <span className="block truncate">{nodeType.title || nodeType.type}</span>
    </button>
  );
}

function clampMenuCoordinate(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), Math.max(min, max));
}

function ContextMenuAction({
  label,
  tone,
  onClick,
}: {
  label: string;
  tone?: "destructive";
  onClick: () => void;
}) {
  return (
    <button
      className={`block w-full px-3 py-2 text-left text-sm hover:bg-accent ${
        tone === "destructive" ? "text-destructive" : ""
      }`}
      onClick={onClick}
    >
      {label}
    </button>
  );
}

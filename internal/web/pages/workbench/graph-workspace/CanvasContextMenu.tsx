import { FileJson, Plus, Trash2 } from "lucide-react";
import type { ReactNode } from "react";
import type { NodePosition } from "../../../lib/graphEditor";
import type { GraphNodeSpec, NodeTypeSchema } from "../../../types";
import { virtualNodeTypes } from "./constants";
import type { CanvasContextMenu as CanvasContextMenuState, VirtualNodeKind } from "./types";

interface CanvasContextMenuProps {
  contextMenu: CanvasContextMenuState;
  paletteNodeTypes: NodeTypeSchema[];
  onAddNode: (nodeType: NodeTypeSchema, position?: NodePosition) => void;
  onAddVirtualNode: (kind: VirtualNodeKind, position?: NodePosition) => void;
  onClose: () => void;
  onDeleteEdge: (edgeId: string) => void;
  onDeleteNode: (nodeId: string) => void;
}

export function CanvasContextMenu({
  contextMenu,
  paletteNodeTypes,
  onAddNode,
  onAddVirtualNode,
  onClose,
  onDeleteEdge,
  onDeleteNode,
}: CanvasContextMenuProps) {
  return (
    <div
      className="fixed z-50 w-64 overflow-hidden rounded-md border border-border bg-panel shadow-lg"
      style={{ left: contextMenu.screen.x, top: contextMenu.screen.y }}
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
              subtitle={`virtual:${nodeType.type}`}
              onClick={() => onAddVirtualNode(nodeType.type as VirtualNodeKind, contextMenu.position)}
            />
          ))}
          {paletteNodeTypes.slice(0, 8).map((nodeType) => (
            <CreateNodeItem
              key={nodeType.type}
              nodeType={nodeType}
              subtitle={nodeType.type}
              onClick={() => onAddNode(nodeType, contextMenu.position)}
            />
          ))}
        </div>
      ) : null}

      {contextMenu.kind === "node" ? (
        <div>
          <ContextMenuTitle>Node</ContextMenuTitle>
          <ContextMenuAction icon={<FileJson className="h-4 w-4 text-muted-foreground" />} label="Edit" onClick={onClose} />
          <ContextMenuAction
            icon={<Trash2 className="h-4 w-4" />}
            label="Delete"
            tone="destructive"
            onClick={() => onDeleteNode(contextMenu.nodeId)}
          />
        </div>
      ) : null}

      {contextMenu.kind === "edge" ? (
        <div>
          <ContextMenuTitle>Edge</ContextMenuTitle>
          <ContextMenuAction icon={<FileJson className="h-4 w-4 text-muted-foreground" />} label="Edit" onClick={onClose} />
          <ContextMenuAction
            icon={<Trash2 className="h-4 w-4" />}
            label="Delete"
            tone="destructive"
            onClick={() => onDeleteEdge(contextMenu.edgeId)}
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
  subtitle,
  onClick,
}: {
  nodeType: Pick<GraphNodeSpec, "type" | "name"> & Pick<NodeTypeSchema, "title">;
  subtitle: string;
  onClick: () => void;
}) {
  return (
    <button className="flex w-full min-w-0 items-center gap-2 px-3 py-2 text-left hover:bg-accent" onClick={onClick}>
      <Plus className="h-4 w-4 text-muted-foreground" />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{nodeType.title || nodeType.type}</span>
        <span className="block truncate font-mono text-[11px] text-muted-foreground">{subtitle}</span>
      </span>
    </button>
  );
}

function ContextMenuAction({
  icon,
  label,
  tone,
  onClick,
}: {
  icon: ReactNode;
  label: string;
  tone?: "destructive";
  onClick: () => void;
}) {
  return (
    <button
      className={`flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-accent ${
        tone === "destructive" ? "text-destructive" : ""
      }`}
      onClick={onClick}
    >
      {icon}
      {label}
    </button>
  );
}

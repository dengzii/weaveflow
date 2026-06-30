import type { NodePosition } from "../../../lib/graphEditor";
import type { GraphNodeSpec, NodeTypeSchema } from "../../../types";
import { virtualNodeTypes } from "./constants";
import type { CanvasContextMenu as CanvasContextMenuState, VirtualNodeKind } from "./types";

interface CanvasContextMenuProps {
  contextMenu: CanvasContextMenuState;
  paletteNodeTypes: NodeTypeSchema[];
  onAddLoop: (position?: NodePosition) => void;
  onAddNode: (nodeType: NodeTypeSchema, position?: NodePosition) => void;
  onAddVirtualNode: (kind: VirtualNodeKind, position?: NodePosition) => void;
  onClose: () => void;
  onDeleteEdge: (edgeId: string) => void;
  onDeleteLoop: (loopId: string) => void;
  onDeleteNode: (nodeId: string) => void;
}

export function CanvasContextMenu({
  contextMenu,
  paletteNodeTypes,
  onAddLoop,
  onAddNode,
  onAddVirtualNode,
  onClose,
  onDeleteEdge,
  onDeleteLoop,
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
          <ContextMenuAction
            label="Create loop"
            onClick={() => onAddLoop(contextMenu.position)}
          />
          {virtualNodeTypes.map((nodeType) => (
            <CreateNodeItem
              key={nodeType.type}
              nodeType={nodeType}
              onClick={() => onAddVirtualNode(nodeType.type as VirtualNodeKind, contextMenu.position)}
            />
          ))}
          {paletteNodeTypes.slice(0, 8).map((nodeType) => (
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

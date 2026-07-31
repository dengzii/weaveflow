import { useState, type RefObject } from "react";
import { ChevronRight } from "lucide-react";
import type { NodePosition } from "../../../lib/graphEditor";
import { partitionNodeTypes } from "../../../lib/nodeGroups";
import type { GraphNodeSpec, NodeGroup, NodeTypeSchema, TriggerType } from "../../../types";
import { virtualNodeTypes } from "./constants";
import type { CanvasContextMenu as CanvasContextMenuState, VirtualNodeKind } from "./types";
import { useCanvasContextMenuLayout } from "./useCanvasContextMenuLayout";

const triggerGroupKey = "__weaveflow_trigger_group__";

interface CanvasContextMenuProps {
  boundaryRef: RefObject<HTMLElement | null>;
  contextMenu: CanvasContextMenuState;
  canCreateTrigger: boolean;
  nodeGroups: NodeGroup[];
  paletteNodeTypes: NodeTypeSchema[];
  onAddNode: (nodeType: NodeTypeSchema, position?: NodePosition) => void;
  onAddVirtualNode: (kind: VirtualNodeKind, position?: NodePosition) => void;
  onCreateTrigger: (type: TriggerType, position: NodePosition) => void;
  onClose: () => void;
  onDeleteEdge: (edgeId: string) => void;
  onDeleteLoop: (loopId: string) => void;
  onDeleteNode: (nodeId: string) => void;
  onDeleteTrigger: (triggerId: string) => void;
  onEditTrigger: (triggerId: string) => void;
  onToggleTrigger: (triggerId: string, enabled: boolean) => void;
}

export function CanvasContextMenu({
  boundaryRef,
  contextMenu,
  canCreateTrigger,
  nodeGroups,
  paletteNodeTypes,
  onAddNode,
  onAddVirtualNode,
  onCreateTrigger,
  onClose,
  onDeleteEdge,
  onDeleteLoop,
  onDeleteNode,
  onDeleteTrigger,
  onEditTrigger,
  onToggleTrigger,
}: CanvasContextMenuProps) {
  const [openGroupName, setOpenGroupName] = useState<string | null>(null);
  const {
    menuRef,
    submenuRef,
    groupButtonRefs,
    layout,
    submenuLayout,
    resetSubmenuLayout,
  } = useCanvasContextMenuLayout(boundaryRef, contextMenu, openGroupName);
  const { groups: groupedPaletteNodeTypes, ungroupedNodeTypes } = partitionNodeTypes(paletteNodeTypes, nodeGroups);
  const openGroup = groupedPaletteNodeTypes.find((group) => group.name === openGroupName) ?? null;
  const triggerGroupOpen = openGroupName === triggerGroupKey;

  const openNodeGroup = (name: string) => {
    if (name !== openGroupName) resetSubmenuLayout();
    setOpenGroupName(name);
  };

  return (
    <>
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
                onMouseEnter={() => setOpenGroupName(null)}
                onClick={() => onAddVirtualNode(nodeType.type as VirtualNodeKind, contextMenu.position)}
              />
            ))}
            {ungroupedNodeTypes.map((nodeType) => (
              <CreateNodeItem
                key={nodeType.type}
                nodeType={nodeType}
                onMouseEnter={() => setOpenGroupName(null)}
                onClick={() => onAddNode(nodeType, contextMenu.position)}
              />
            ))}
            <CreateNodeGroupItem
              buttonRef={(element) => {
                if (element) groupButtonRefs.current.set(triggerGroupKey, element);
                else groupButtonRefs.current.delete(triggerGroupKey);
              }}
              name="Trigger"
              open={triggerGroupOpen}
              disabled={!canCreateTrigger}
              onOpen={() => openNodeGroup(triggerGroupKey)}
            />
            {groupedPaletteNodeTypes.map((group) => (
              <CreateNodeGroupItem
                key={group.name}
                buttonRef={(element) => {
                  if (element) groupButtonRefs.current.set(group.name, element);
                  else groupButtonRefs.current.delete(group.name);
                }}
                name={group.name}
                open={group.name === openGroupName}
                onOpen={() => openNodeGroup(group.name)}
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

        {contextMenu.kind === "trigger" ? (
          <div>
            <ContextMenuTitle>Trigger</ContextMenuTitle>
            <ContextMenuAction label="Edit" onClick={() => onEditTrigger(contextMenu.triggerId)} />
            <ContextMenuAction
              label={contextMenu.enabled ? "Disable" : "Enable"}
              onClick={() => onToggleTrigger(contextMenu.triggerId, !contextMenu.enabled)}
            />
            <ContextMenuAction
              label="Delete"
              tone="destructive"
              onClick={() => onDeleteTrigger(contextMenu.triggerId)}
            />
          </div>
        ) : null}
      </div>

      {contextMenu.kind === "pane" && (triggerGroupOpen || openGroup) ? (
        <div
          ref={submenuRef}
          className="fixed z-[60] max-h-[calc(100vh-1rem)] w-64 overflow-y-auto rounded-md border border-border bg-panel shadow-lg"
          style={{
            left: submenuLayout?.left ?? 0,
            top: submenuLayout?.top ?? 0,
            maxHeight: submenuLayout ? `${submenuLayout.maxHeight}px` : undefined,
            maxWidth: submenuLayout ? `${submenuLayout.maxWidth}px` : undefined,
            visibility: submenuLayout ? "visible" : "hidden",
          }}
          onClick={(event) => event.stopPropagation()}
          onContextMenu={(event) => event.preventDefault()}
        >
          <ContextMenuTitle>{triggerGroupOpen ? "Trigger" : openGroup?.name ?? ""}</ContextMenuTitle>
          {triggerGroupOpen ? (
            <>
              <CreateNodeItem
                nodeType={{ type: "webhook", title: "Webhook" }}
                onClick={() => onCreateTrigger("webhook", contextMenu.position)}
              />
              <CreateNodeItem
                nodeType={{ type: "schedule", title: "Schedule" }}
                onClick={() => onCreateTrigger("schedule", contextMenu.position)}
              />
              <CreateNodeItem
                nodeType={{ type: "chat", title: "Chat" }}
                onClick={() => onCreateTrigger("chat", contextMenu.position)}
              />
            </>
          ) : (
            openGroup?.nodeTypes.map((nodeType) => (
              <CreateNodeItem
                key={nodeType.type}
                nodeType={nodeType}
                onClick={() => onAddNode(nodeType, contextMenu.position)}
              />
            ))
          )}
        </div>
      ) : null}
    </>
  );
}

function ContextMenuTitle({ children }: { children: string }) {
  return <div className="border-b border-border px-3 py-2 text-xs font-semibold uppercase text-muted-foreground">{children}</div>;
}

function CreateNodeItem({
  nodeType,
  onMouseEnter,
  onClick,
}: {
  nodeType: Pick<GraphNodeSpec, "type" | "name"> & Pick<NodeTypeSchema, "title">;
  onMouseEnter?: () => void;
  onClick: () => void;
}) {
  return (
    <button
      className="block w-full min-w-0 px-3 py-2 text-left text-sm font-medium hover:bg-accent"
      onMouseEnter={onMouseEnter}
      onFocus={onMouseEnter}
      onClick={onClick}
    >
      <span className="block truncate">{nodeType.title || nodeType.type}</span>
    </button>
  );
}

function CreateNodeGroupItem({
  buttonRef,
  name,
  open,
  disabled = false,
  onOpen,
}: {
  buttonRef: (element: HTMLButtonElement | null) => void;
  name: string;
  open: boolean;
  disabled?: boolean;
  onOpen: () => void;
}) {
  return (
    <button
      ref={buttonRef}
      className={`flex w-full min-w-0 items-center gap-2 px-3 py-2 text-left text-sm font-medium hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50 ${
        open ? "bg-accent" : ""
      }`}
      aria-expanded={open}
      aria-haspopup="menu"
      disabled={disabled}
      onMouseEnter={onOpen}
      onFocus={onOpen}
      onClick={onOpen}
    >
      <span className="min-w-0 flex-1 truncate">{name}</span>
      <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
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

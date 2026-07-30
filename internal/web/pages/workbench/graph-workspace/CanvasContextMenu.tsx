import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import { ChevronRight } from "lucide-react";
import type { NodePosition } from "../../../lib/graphEditor";
import { partitionNodeTypes } from "../../../lib/nodeGroups";
import type { GraphNodeSpec, NodeGroup, NodeTypeSchema, TriggerType } from "../../../types";
import { virtualNodeTypes } from "./constants";
import type { CanvasContextMenu as CanvasContextMenuState, VirtualNodeKind } from "./types";

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

interface MenuLayout {
  left: number;
  top: number;
  maxHeight: number;
  maxWidth: number;
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
  const menuRef = useRef<HTMLDivElement | null>(null);
  const submenuRef = useRef<HTMLDivElement | null>(null);
  const groupButtonRefs = useRef(new Map<string, HTMLButtonElement>());
  const [layout, setLayout] = useState<MenuLayout | null>(null);
  const [submenuLayout, setSubmenuLayout] = useState<MenuLayout | null>(null);
  const [openGroupName, setOpenGroupName] = useState<string | null>(null);
  const { groups: groupedPaletteNodeTypes, ungroupedNodeTypes } = partitionNodeTypes(paletteNodeTypes, nodeGroups);
  const openGroup = groupedPaletteNodeTypes.find((group) => group.name === openGroupName) ?? null;
  const triggerGroupOpen = openGroupName === triggerGroupKey;

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

  useLayoutEffect(() => {
    const submenu = submenuRef.current;
    const anchor = openGroupName ? groupButtonRefs.current.get(openGroupName) : null;
    if (!submenu || !anchor) {
      setSubmenuLayout(null);
      return;
    }

    const updateLayout = () => {
      const boundary = boundaryRef.current?.getBoundingClientRect();
      const anchorRect = anchor.getBoundingClientRect();
      const margin = 8;
      const leftBound = Math.max(margin, (boundary?.left ?? 0) + margin);
      const topBound = Math.max(margin, (boundary?.top ?? 0) + margin);
      const rightBound = Math.min(window.innerWidth - margin, (boundary?.right ?? window.innerWidth) - margin);
      const bottomBound = Math.min(window.innerHeight - margin, (boundary?.bottom ?? window.innerHeight) - margin);
      const maxWidth = Math.max(0, rightBound - leftBound);
      const maxHeight = Math.max(0, bottomBound - topBound);
      const submenuWidth = Math.min(submenu.getBoundingClientRect().width, maxWidth);
      const submenuHeight = Math.min(submenu.scrollHeight, maxHeight);
      const preferredLeft = anchorRect.right + submenuWidth > rightBound ? anchorRect.left - submenuWidth : anchorRect.right;
      const left = clampMenuCoordinate(preferredLeft, leftBound, rightBound - submenuWidth);
      const top = clampMenuCoordinate(anchorRect.top, topBound, bottomBound - submenuHeight);
      const nextLayout = { left, top, maxHeight, maxWidth };
      setSubmenuLayout((current) =>
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
    menuRef.current?.addEventListener("scroll", updateLayout);
    const resizeObserver = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(updateLayout);
    resizeObserver?.observe(anchor);
    resizeObserver?.observe(submenu);
    if (boundaryRef.current) resizeObserver?.observe(boundaryRef.current);
    return () => {
      window.removeEventListener("resize", updateLayout);
      menuRef.current?.removeEventListener("scroll", updateLayout);
      resizeObserver?.disconnect();
    };
  }, [boundaryRef, contextMenu, layout, openGroupName]);

  const openNodeGroup = (name: string) => {
    if (name !== openGroupName) setSubmenuLayout(null);
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

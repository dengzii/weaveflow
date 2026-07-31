import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import type { NodePosition } from "../../../lib/graphEditor";
import type { CanvasContextMenu } from "./types";

interface MenuLayout {
  left: number;
  top: number;
  maxHeight: number;
  maxWidth: number;
}

interface Rectangle {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

interface MenuSize {
  width: number;
  height: number;
}

const menuMargin = 8;

export function rootContextMenuLayout(
  screen: NodePosition,
  menuSize: MenuSize,
  viewport: { width: number; height: number },
  boundary?: Rectangle
): MenuLayout {
  const bounds = availableMenuBounds(viewport, boundary);
  const width = Math.min(menuSize.width, bounds.maxWidth);
  const height = Math.min(menuSize.height, bounds.maxHeight);
  const preferredLeft = screen.x + width > bounds.right ? screen.x - width : screen.x;
  const preferredTop = screen.y + height > bounds.bottom ? screen.y - height : screen.y;
  return {
    left: clampMenuCoordinate(preferredLeft, bounds.left, bounds.right - width),
    top: clampMenuCoordinate(preferredTop, bounds.top, bounds.bottom - height),
    maxHeight: bounds.maxHeight,
    maxWidth: bounds.maxWidth,
  };
}

export function submenuContextMenuLayout(
  anchor: Rectangle,
  menuSize: MenuSize,
  viewport: { width: number; height: number },
  boundary?: Rectangle
): MenuLayout {
  const bounds = availableMenuBounds(viewport, boundary);
  const width = Math.min(menuSize.width, bounds.maxWidth);
  const height = Math.min(menuSize.height, bounds.maxHeight);
  const preferredLeft = anchor.right + width > bounds.right ? anchor.left - width : anchor.right;
  return {
    left: clampMenuCoordinate(preferredLeft, bounds.left, bounds.right - width),
    top: clampMenuCoordinate(anchor.top, bounds.top, bounds.bottom - height),
    maxHeight: bounds.maxHeight,
    maxWidth: bounds.maxWidth,
  };
}

export function useCanvasContextMenuLayout(
  boundaryRef: RefObject<HTMLElement | null>,
  contextMenu: CanvasContextMenu,
  openGroupName: string | null
) {
  const menuRef = useRef<HTMLDivElement | null>(null);
  const submenuRef = useRef<HTMLDivElement | null>(null);
  const groupButtonRefs = useRef(new Map<string, HTMLButtonElement>());
  const [layout, setLayout] = useState<MenuLayout | null>(null);
  const [submenuLayout, setSubmenuLayout] = useState<MenuLayout | null>(null);

  useLayoutEffect(() => {
    const menu = menuRef.current;
    if (!menu) return;
    const boundaryElement = boundaryRef.current;

    const updateLayout = () => {
      const next = rootContextMenuLayout(
        contextMenu.screen,
        { width: menu.getBoundingClientRect().width, height: menu.scrollHeight },
        { width: window.innerWidth, height: window.innerHeight },
        boundaryElement?.getBoundingClientRect()
      );
      setLayout((current) => sameMenuLayout(current, next) ? current : next);
    };

    updateLayout();
    window.addEventListener("resize", updateLayout);
    const resizeObserver = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(updateLayout);
    resizeObserver?.observe(menu);
    if (boundaryElement) resizeObserver?.observe(boundaryElement);
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
    const boundaryElement = boundaryRef.current;
    const scrollContainer = menuRef.current;

    const updateLayout = () => {
      const next = submenuContextMenuLayout(
        anchor.getBoundingClientRect(),
        { width: submenu.getBoundingClientRect().width, height: submenu.scrollHeight },
        { width: window.innerWidth, height: window.innerHeight },
        boundaryElement?.getBoundingClientRect()
      );
      setSubmenuLayout((current) => sameMenuLayout(current, next) ? current : next);
    };

    updateLayout();
    window.addEventListener("resize", updateLayout);
    scrollContainer?.addEventListener("scroll", updateLayout);
    const resizeObserver = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(updateLayout);
    resizeObserver?.observe(anchor);
    resizeObserver?.observe(submenu);
    if (boundaryElement) resizeObserver?.observe(boundaryElement);
    return () => {
      window.removeEventListener("resize", updateLayout);
      scrollContainer?.removeEventListener("scroll", updateLayout);
      resizeObserver?.disconnect();
    };
  }, [boundaryRef, contextMenu, layout, openGroupName]);

  return {
    menuRef,
    submenuRef,
    groupButtonRefs,
    layout,
    submenuLayout,
    resetSubmenuLayout: () => setSubmenuLayout(null),
  };
}

function availableMenuBounds(viewport: { width: number; height: number }, boundary?: Rectangle) {
  const left = Math.max(menuMargin, (boundary?.left ?? 0) + menuMargin);
  const top = Math.max(menuMargin, (boundary?.top ?? 0) + menuMargin);
  const right = Math.min(viewport.width - menuMargin, (boundary?.right ?? viewport.width) - menuMargin);
  const bottom = Math.min(viewport.height - menuMargin, (boundary?.bottom ?? viewport.height) - menuMargin);
  return {
    left,
    top,
    right,
    bottom,
    maxWidth: Math.max(0, right - left),
    maxHeight: Math.max(0, bottom - top),
  };
}

function clampMenuCoordinate(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), Math.max(min, max));
}

function sameMenuLayout(current: MenuLayout | null, next: MenuLayout): boolean {
  return Boolean(
    current &&
      current.left === next.left &&
      current.top === next.top &&
      current.maxHeight === next.maxHeight &&
      current.maxWidth === next.maxWidth
  );
}

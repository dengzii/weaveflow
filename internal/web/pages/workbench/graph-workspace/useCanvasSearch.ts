import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { GraphNodeSpec } from "../../../types";

export function matchingCanvasNodes(nodes: readonly GraphNodeSpec[], query: string): GraphNodeSpec[] {
  const normalizedQuery = query.trim().toLowerCase();
  if (!normalizedQuery) return [];
  return nodes.filter((node) => (
    `${node.id} ${node.name ?? ""} ${node.type ?? ""} ${node.description ?? ""}`
      .toLowerCase()
      .includes(normalizedQuery)
  ));
}

export function nextCanvasSearchIndex(currentIndex: number, direction: 1 | -1, matchCount: number): number {
  if (matchCount <= 0) return 0;
  return (currentIndex + direction + matchCount) % matchCount;
}

export function useCanvasSearch(nodes: readonly GraphNodeSpec[], onFocusNode: (nodeID: string) => void) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [matchIndex, setMatchIndex] = useState(0);
  const [focusNodeID, setFocusNodeID] = useState<string>();
  const [focusNodeSignal, setFocusNodeSignal] = useState(0);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const onFocusNodeRef = useRef(onFocusNode);
  onFocusNodeRef.current = onFocusNode;

  const matches = useMemo(() => matchingCanvasNodes(nodes, query), [nodes, query]);
  const highlightedNodeIDs = useMemo(() => matches.map((node) => node.id), [matches]);

  const focusNode = useCallback((nodeID: string) => {
    onFocusNodeRef.current(nodeID);
    setFocusNodeID(nodeID);
    setFocusNodeSignal((value) => value + 1);
  }, []);

  useEffect(() => {
    if (!open) return;
    const timer = window.setTimeout(() => inputRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, [open]);

  useEffect(() => {
    if (!open || !query.trim()) return;
    setMatchIndex(0);
    const first = matches[0];
    if (first) focusNode(first.id);
  }, [focusNode, matches, open, query]);

  const move = useCallback((direction: 1 | -1) => {
    if (matches.length === 0) return;
    const nextIndex = nextCanvasSearchIndex(matchIndex, direction, matches.length);
    setMatchIndex(nextIndex);
    focusNode(matches[nextIndex].id);
  }, [focusNode, matchIndex, matches]);

  return {
    open,
    query,
    matches,
    matchIndex,
    inputRef,
    focusNodeID,
    focusNodeSignal,
    highlightedNodeIDs,
    setOpen,
    setQuery,
    move,
    focusNode,
  };
}

export type CanvasSearchController = ReturnType<typeof useCanvasSearch>;

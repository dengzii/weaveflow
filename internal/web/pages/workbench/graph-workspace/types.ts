import type { NodePosition } from "../../../lib/graphEditor";

export type CanvasContextMenu =
  | { kind: "pane"; screen: NodePosition; position: NodePosition }
  | { kind: "node"; screen: NodePosition; nodeId: string }
  | { kind: "edge"; screen: NodePosition; edgeId: string };

export type VirtualNodeKind = "start" | "end";

export type InspectorMode = "graph" | "node" | "edge" | "virtual";

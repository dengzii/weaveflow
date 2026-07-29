import type { NodePosition } from "../../../lib/graphEditor";

export type CanvasContextMenu =
  | { kind: "pane"; screen: NodePosition; position: NodePosition }
  | { kind: "node"; screen: NodePosition; nodeId: string }
  | { kind: "edge"; screen: NodePosition; edgeId: string }
  | { kind: "loop"; screen: NodePosition; loopId: string }
  | { kind: "trigger"; screen: NodePosition; triggerId: string; enabled: boolean };

export type VirtualNodeKind = "start" | "end";

export type InspectorMode = "graph" | "node" | "edge" | "virtual" | "loop";

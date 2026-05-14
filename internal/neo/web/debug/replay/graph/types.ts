export interface ArtifactDetail {
  bytes: number;
  encoding: "text" | "json" | "base64";
  payload: unknown;
  truncated?: boolean;
}

export interface StatePathValueEntry {
  path: string;
  value: unknown;
}

export interface GraphNodeMeta {
  id: string;
  name: string;
  type: string;
  description: string;
  config: Record<string, unknown> | null;
}

export interface GraphEdgeMeta {
  id: string;
  from: string;
  to: string;
  label: string;
  conditional: boolean;
}

export interface SourceGraph {
  entry_point?: string;
  finish_point?: string;
  nodes: GraphNodeMeta[];
  edges: GraphEdgeMeta[];
}

export interface NodeArtifactRef {
  id: string;
  type: string;
  mimeType: string;
}

export interface StatePatchSummary {
  eventCount: number;
  changeCount: number;
  lastPaths: string[];
}

export interface NodeEventSummary {
  durationMs: number;
  llmReasoning: string;
  llmContent: string;
  tokenUsage: {
    promptTokens: number;
    completionTokens: number;
    totalTokens: number;
    reasoningTokens: number;
    promptCachedTokens: number;
  };
  functionCalls: { name: string; args: string }[];
  toolCalls: { name: string; status: "pending" | "done" | "failed" }[];
  contractInput?: NodeArtifactRef;
  contractOutputPatch?: NodeArtifactRef;
  contractMergedState?: NodeArtifactRef;
  statePatch: StatePatchSummary;
  artifacts: NodeArtifactRef[];
}

export interface GraphProjection {
  currentNodeId: string;
  currentEdgeId: string;
  visitedNodeIds: Set<string>;
  completedNodeIds: Set<string>;
  failedNodeIds: Set<string>;
  traversedEdgeIds: Set<string>;
  nodeEventSummaries: Map<string, NodeEventSummary>;
}

export interface FlowNodeData {
  label: import("react").ReactNode;
  meta: GraphNodeMeta;
  elkHeight: number;
  elkWidth: number;
}

export const SYNTHETIC_START_ID = "__wf_start__";
export const SYNTHETIC_END_ID = "__wf_end__";

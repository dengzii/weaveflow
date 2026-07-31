export type GraphLintSeverity = "error" | "warn";

export interface GraphLintIssue {
  id: string;
  severity: GraphLintSeverity;
  message: string;
  nodeID?: string;
  edgeID?: string;
  path?: string;
}

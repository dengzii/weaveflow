import type {
  ApiResponse,
  ArtifactDetail,
  ArtifactRef,
  CachedGraphSummary,
  CheckpointDetail,
  CheckpointRecord,
  GraphDefinition,
  GraphInfo,
  GraphLoadResult,
  InitialStateRequirements,
  RegistryInfo,
  RunDetail,
  RunRecord,
  RunResult,
  RuntimeEvent,
  StepRecord,
} from "./types";

async function readResponse<T>(resp: Response): Promise<T> {
  const contentType = resp.headers.get("content-type") ?? "";
  const text = await resp.text();
  let payload: ApiResponse<T> | undefined;
  if (contentType.includes("application/json") && text) {
    payload = JSON.parse(text) as ApiResponse<T>;
  }
  if (!resp.ok) {
    throw new Error(payload?.error || text || resp.statusText);
  }
  if (payload && "data" in payload) {
    return payload.data as T;
  }
  return payload as T;
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, init);
  return readResponse<T>(resp);
}

function graphQuery(graphId?: string): string {
  if (!graphId) return "";
  return `?graph_id=${encodeURIComponent(graphId)}`;
}

function appendGraphQuery(path: string, graphId?: string): string {
  if (!graphId) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}graph_id=${encodeURIComponent(graphId)}`;
}

export async function getGraphInfo(): Promise<GraphInfo> {
  return apiFetch<GraphInfo>("/graph");
}

export async function getGraphDefinition(): Promise<GraphDefinition> {
  return apiFetch<GraphDefinition>("/graph/definition");
}

export async function getInitialStateRequirements(): Promise<InitialStateRequirements> {
  return apiFetch<InitialStateRequirements>("/graph/initial-state-requirements");
}

export async function analyzeInitialStateRequirements(
  definition: GraphDefinition,
  graphId?: string,
  graphVersion?: string
): Promise<InitialStateRequirements> {
  return apiFetch<InitialStateRequirements>("/graph/initial-state-requirements", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      graph_id: graphId || undefined,
      graph_version: graphVersion || undefined,
      definition,
    }),
  });
}

export async function setGraphDefinition(
  definition: GraphDefinition,
  graphId?: string,
  graphVersion?: string
): Promise<GraphLoadResult> {
  return apiFetch<GraphLoadResult>("/graph", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      graph_id: graphId || undefined,
      graph_version: graphVersion || undefined,
      definition,
    }),
  });
}

export async function getRegistry(): Promise<RegistryInfo> {
  return apiFetch<RegistryInfo>("/registry");
}

export async function listGraphs(): Promise<CachedGraphSummary[]> {
  return apiFetch<CachedGraphSummary[]>("/graphs");
}

export async function listRuns(graphId?: string): Promise<RunRecord[]> {
  return apiFetch<RunRecord[]>(`/runs${graphQuery(graphId)}`);
}

export async function startRun(initialState: unknown): Promise<RunResult> {
  return apiFetch<RunResult>("/runs", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ initial_state: initialState }),
  });
}

export async function resumeRun(runId: string, input: unknown): Promise<RunResult> {
  return apiFetch<RunResult>(`/runs/${encodeURIComponent(runId)}/resume`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ input }),
  });
}

export async function resumeCheckpoint(checkpointId: string, input: unknown): Promise<RunResult> {
  return apiFetch<RunResult>(`/checkpoints/${encodeURIComponent(checkpointId)}/resume`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ input }),
  });
}

export async function getRunDetail(runId: string, graphId?: string): Promise<RunDetail> {
  return apiFetch<RunDetail>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/detail`, graphId));
}

export async function pauseRun(runId: string): Promise<RunRecord> {
  return apiFetch<RunRecord>(`/runs/${encodeURIComponent(runId)}/pause`, { method: "POST" });
}

export async function cancelRun(runId: string, graphId?: string): Promise<RunRecord> {
  return apiFetch<RunRecord>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/cancel`, graphId), { method: "POST" });
}

export async function deleteRun(runId: string, graphId?: string): Promise<RunRecord> {
  return apiFetch<RunRecord>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}`, graphId), { method: "DELETE" });
}

export async function listSteps(runId: string, graphId?: string): Promise<StepRecord[]> {
  return apiFetch<StepRecord[]>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/steps`, graphId));
}

export async function listCheckpoints(runId: string, graphId?: string): Promise<CheckpointRecord[]> {
  return apiFetch<CheckpointRecord[]>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/checkpoints`, graphId));
}

export async function getCheckpoint(checkpointId: string, graphId?: string): Promise<CheckpointDetail> {
  return apiFetch<CheckpointDetail>(appendGraphQuery(`/checkpoints/${encodeURIComponent(checkpointId)}`, graphId));
}

export async function listArtifacts(runId: string, graphId?: string): Promise<ArtifactRef[]> {
  return apiFetch<ArtifactRef[]>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/artifacts`, graphId));
}

export async function getArtifact(runId: string, artifactId: string, graphId?: string): Promise<ArtifactDetail> {
  return apiFetch<ArtifactDetail>(
    appendGraphQuery(`/runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(artifactId)}`, graphId)
  );
}

export async function listEvents(runId: string, graphId?: string): Promise<RuntimeEvent[]> {
  return apiFetch<RuntimeEvent[]>(appendGraphQuery(`/runs/${encodeURIComponent(runId)}/events`, graphId));
}

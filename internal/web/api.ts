import type {
  ApiResponse,
  ArtifactDetail,
  ArtifactRef,
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

export async function listRuns(): Promise<RunRecord[]> {
  return apiFetch<RunRecord[]>("/runs");
}

export async function startRun(initialState: unknown): Promise<RunResult> {
  return apiFetch<RunResult>("/runs", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ initial_state: initialState }),
  });
}

export async function getRunDetail(runId: string): Promise<RunDetail> {
  return apiFetch<RunDetail>(`/runs/${encodeURIComponent(runId)}/detail`);
}

export async function pauseRun(runId: string): Promise<RunRecord> {
  return apiFetch<RunRecord>(`/runs/${encodeURIComponent(runId)}/pause`, { method: "POST" });
}

export async function cancelRun(runId: string): Promise<RunRecord> {
  return apiFetch<RunRecord>(`/runs/${encodeURIComponent(runId)}/cancel`, { method: "POST" });
}

export async function listSteps(runId: string): Promise<StepRecord[]> {
  return apiFetch<StepRecord[]>(`/runs/${encodeURIComponent(runId)}/steps`);
}

export async function listCheckpoints(runId: string): Promise<CheckpointRecord[]> {
  return apiFetch<CheckpointRecord[]>(`/runs/${encodeURIComponent(runId)}/checkpoints`);
}

export async function getCheckpoint(checkpointId: string): Promise<CheckpointDetail> {
  return apiFetch<CheckpointDetail>(`/checkpoints/${encodeURIComponent(checkpointId)}`);
}

export async function listArtifacts(runId: string): Promise<ArtifactRef[]> {
  return apiFetch<ArtifactRef[]>(`/runs/${encodeURIComponent(runId)}/artifacts`);
}

export async function getArtifact(runId: string, artifactId: string): Promise<ArtifactDetail> {
  return apiFetch<ArtifactDetail>(
    `/runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(artifactId)}`
  );
}

export async function listEvents(runId: string): Promise<RuntimeEvent[]> {
  return apiFetch<RuntimeEvent[]>(`/runs/${encodeURIComponent(runId)}/events`);
}

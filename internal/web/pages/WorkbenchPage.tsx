import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  analyzeInitialStateRequirements,
  cancelRun,
  getArtifact,
  getCheckpoint,
  getGraphDefinition,
  getGraphInfo,
  getInitialStateRequirements,
  getRegistry,
  listRuns,
  getRunDetail,
  pauseRun,
  setGraphDefinition,
  startRun,
} from "../api";
import { parseJSON, stringifyJSON } from "../lib/utils";
import { pickInitialLocalGraphDraft, readLocalGraphDrafts } from "../lib/localGraphs";
import {
  defaultInitialState,
  runtimeEventTypes,
  sampleGraph,
  workspaceTabs,
  type WorkspaceTab,
} from "./workbench/constants";
import { GraphWorkspace } from "./workbench/GraphWorkspace";
import { ManageWorkspace } from "./workbench/ManageWorkspace";
import { ObserveWorkspace } from "./workbench/ObserveWorkspace";
import { RunStatusPanel } from "./workbench/RunStatusPanel";
import { RunsWorkspace } from "./workbench/RunsWorkspace";
import { SettingsWorkspace } from "./workbench/SettingsWorkspace";
import { WorkbenchShell } from "./workbench/WorkbenchShell";
import type {
  ArtifactDetail,
  ArtifactRef,
  CheckpointDetail,
  CheckpointRecord,
  GraphDefinition,
  GraphInfo,
  InitialStateRequirement,
  InitialStateRequirements,
  RegistryInfo,
  RunRecord,
  RuntimeEvent,
  StepRecord,
} from "../types";

export { workspaceTabs };
export type { WorkspaceTab };

export function WorkbenchPage({
  tab: controlledTab,
  onTabChange,
}: {
  tab?: WorkspaceTab;
  onTabChange?: (tab: WorkspaceTab) => void;
}) {
  const [localTab, setLocalTab] = useState<WorkspaceTab>("graph");
  const tab = controlledTab ?? localTab;
  const setTab = useCallback(
    (nextTab: WorkspaceTab) => {
      if (!controlledTab) setLocalTab(nextTab);
      onTabChange?.(nextTab);
    },
    [controlledTab, onTabChange]
  );
  const [definitionText, setDefinitionText] = useState(stringifyJSON(sampleGraph));
  const [initialStateText, setInitialStateText] = useState(stringifyJSON(defaultInitialState));
  const [graphInfo, setGraphInfo] = useState<GraphInfo | null>(null);
  const [initialRequirements, setInitialRequirements] = useState<InitialStateRequirements | null>(null);
  const [registry, setRegistry] = useState<RegistryInfo | null>(null);
  const [runs, setRuns] = useState<RunRecord[]>([]);
  const [selectedRunId, setSelectedRunId] = useState("");
  const [steps, setSteps] = useState<StepRecord[]>([]);
  const [checkpoints, setCheckpoints] = useState<CheckpointRecord[]>([]);
  const [artifacts, setArtifacts] = useState<ArtifactRef[]>([]);
  const [events, setEvents] = useState<RuntimeEvent[]>([]);
  const [selectedCheckpointId, setSelectedCheckpointId] = useState("");
  const [selectedArtifactId, setSelectedArtifactId] = useState("");
  const [checkpointDetail, setCheckpointDetail] = useState<CheckpointDetail | null>(null);
  const [artifactDetail, setArtifactDetail] = useState<ArtifactDetail | null>(null);
  const [resourceStatus, setResourceStatus] = useState("");
  const [liveEvents, setLiveEvents] = useState<RuntimeEvent[]>([]);
  const [runStatusVisible, setRunStatusVisible] = useState(false);
  const [streamTypes, setStreamTypes] = useState("");
  const [graphId, setGraphId] = useState("debug_graph");
  const [graphVersion, setGraphVersion] = useState("1.0");
  const [initialRequirementsError, setInitialRequirementsError] = useState("");
  const [status, setStatus] = useState("idle");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const preferLocalGraphRef = useRef(false);
  const checkedLocalGraphRef = useRef(false);

  if (!checkedLocalGraphRef.current) {
    checkedLocalGraphRef.current = true;
    preferLocalGraphRef.current = Boolean(pickInitialLocalGraphDraft(readLocalGraphDrafts()));
  }

  const definition = useMemo(() => {
    try {
      return parseJSON<GraphDefinition>(definitionText);
    } catch {
      return null;
    }
  }, [definitionText]);

  const selectedRun = useMemo(
    () => runs.find((run) => run.run_id === selectedRunId) ?? null,
    [runs, selectedRunId]
  );
  const displayEvents = useMemo(() => {
    const seen = new Set<string>();
    return [...liveEvents, ...events]
      .filter((event) => {
        const key = event.id || `${event.run_id}:${event.type}:${event.timestamp}:${event.node_id ?? ""}`;
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      })
      .slice(0, 120);
  }, [events, liveEvents]);

  const refreshRuns = useCallback(async () => {
    const nextRuns = await listRuns();
    setRuns(nextRuns);
    setSelectedRunId((current) => current || nextRuns.at(-1)?.run_id || "");
  }, []);

  const refreshInitialRequirements = useCallback(async () => {
    const requirements = await getInitialStateRequirements();
    setInitialRequirements(requirements);
    setInitialRequirementsError("");
    return requirements;
  }, []);

  const refreshSelectedRun = useCallback(
    async (runId: string) => {
      if (!runId) {
        setSteps([]);
        setCheckpoints([]);
        setArtifacts([]);
        setEvents([]);
        setSelectedCheckpointId("");
        setSelectedArtifactId("");
        setCheckpointDetail(null);
        setArtifactDetail(null);
        setResourceStatus("");
        return;
      }
      setCheckpointDetail(null);
      setArtifactDetail(null);
      setResourceStatus("");
      const detail = await getRunDetail(runId);
      setSteps(detail.steps);
      setCheckpoints(detail.checkpoints);
      setArtifacts(detail.artifacts);
      setEvents(detail.events);
      setRuns((current) => current.map((run) => (run.run_id === detail.run.run_id ? detail.run : run)));
      setSelectedCheckpointId((current) =>
        current && detail.checkpoints.some((checkpoint) => checkpoint.checkpoint_id === current) ? current : ""
      );
      setSelectedArtifactId((current) =>
        current && detail.artifacts.some((artifact) => artifact.id === current) ? current : ""
      );
    },
    []
  );

  const loadServerState = useCallback(async () => {
    setStatus("loading");
    setError("");
    try {
      const [info, reg] = await Promise.all([
        getGraphInfo().catch(() => null),
        getRegistry().catch(() => null),
      ]);
      setGraphInfo(info);
      setRegistry(reg);
      if (info) {
        if (!preferLocalGraphRef.current) {
          setGraphId(info.id);
          setGraphVersion(info.version);
          const def = await getGraphDefinition();
          setDefinitionText(stringifyJSON(def));
          await refreshInitialRequirements().catch((err) => {
            setInitialRequirements(null);
            setInitialRequirementsError(err instanceof Error ? err.message : String(err));
          });
        }
      }
      await refreshRuns().catch(() => undefined);
      setStatus("ready");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
    }
  }, [refreshInitialRequirements, refreshRuns]);

  useEffect(() => {
    void loadServerState();
  }, [loadServerState]);

  const handleLocalGraphLoaded = useCallback(() => {
    preferLocalGraphRef.current = true;
  }, []);

  useEffect(() => {
    if (!definition) {
      setInitialRequirements(null);
      setInitialRequirementsError("");
      return;
    }
    let canceled = false;
    const timer = window.setTimeout(() => {
      void analyzeInitialStateRequirements(definition, graphId, graphVersion)
        .then((requirements) => {
          if (!canceled) {
            setInitialRequirements(requirements);
            setInitialRequirementsError("");
          }
        })
        .catch((err) => {
          if (!canceled) {
            setInitialRequirements(null);
            setInitialRequirementsError(err instanceof Error ? err.message : String(err));
          }
        });
    }, 350);
    return () => {
      canceled = true;
      window.clearTimeout(timer);
    };
  }, [definition, graphId, graphVersion]);

  useEffect(() => {
    void refreshSelectedRun(selectedRunId).catch((err) => {
      setError(err instanceof Error ? err.message : String(err));
    });
  }, [refreshSelectedRun, selectedRunId]);

  useEffect(() => {
    const params = new URLSearchParams();
    if (selectedRunId) params.set("run_id", selectedRunId);
    if (streamTypes.trim()) params.set("type", streamTypes.trim());
    const source = new EventSource(`/events/stream${params.toString() ? `?${params}` : ""}`);
    const onEvent = (message: MessageEvent<string>) => {
      try {
        const event = JSON.parse(message.data) as RuntimeEvent;
        setLiveEvents((current) => [event, ...current].slice(0, 120));
        const nextStatus = runStatusFromEvent(event.type);
        if (nextStatus) {
          setRuns((current) =>
            current.map((run) =>
              run.run_id === event.run_id
                ? {
                    ...run,
                    status: nextStatus,
                    updated_at: event.timestamp,
                    finished_at: isTerminalRunStatus(nextStatus) ? event.timestamp : run.finished_at,
                  }
                : run
            )
          );
        }
      } catch {
        // ignore malformed frames
      }
    };
    source.onmessage = onEvent;
    for (const eventType of runtimeEventTypes) {
      source.addEventListener(eventType, onEvent as EventListener);
    }
    source.onerror = () => {
      source.close();
    };
    return () => {
      for (const eventType of runtimeEventTypes) {
        source.removeEventListener(eventType, onEvent as EventListener);
      }
      source.close();
    };
  }, [selectedRunId, streamTypes]);

  async function uploadGraph() {
    if (!definition) {
      setError("Graph JSON is invalid");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await setGraphDefinition(definition, graphId, graphVersion);
      setGraphInfo(result.graph);
      setDefinitionText(stringifyJSON(result.definition));
      await refreshInitialRequirements().catch((err) => {
        setInitialRequirements(null);
        setInitialRequirementsError(err instanceof Error ? err.message : String(err));
      });
      await refreshRuns();
      setStatus("graph saved");
    } catch (err) {
      setInitialRequirementsError(err instanceof Error ? err.message : String(err));
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function runGraph() {
    setBusy(true);
    setError("");
    try {
      if (!definition) {
        setError("Graph JSON is invalid");
        setStatus("invalid graph");
        return;
      }
      const requirements = await analyzeInitialStateRequirements(definition, graphId, graphVersion);
      setInitialRequirements(requirements);
      setInitialRequirementsError("");

      const initialState = parseJSON<unknown>(initialStateText);
      const missingInitialState = missingInitialStateRequirements(initialState, requirements.required);
      if (missingInitialState.length > 0) {
        const preview = missingInitialState.slice(0, 4).join(", ");
        const suffix = missingInitialState.length > 4 ? ` (+${missingInitialState.length - 4} more)` : "";
        setError(`Missing initial state: ${preview}${suffix}`);
        setStatus("initial state required");
        setTab("graph");
        return;
      }
      if (requirements.unresolved.length > 0) {
        const unresolved = requirements.unresolved.map((item) => item.path).slice(0, 4).join(", ");
        const suffix = requirements.unresolved.length > 4 ? ` (+${requirements.unresolved.length - 4} more)` : "";
        setError(`Unresolved state requirements: ${unresolved}${suffix}`);
        setStatus("state requirements unresolved");
        setTab("graph");
        return;
      }

      await setGraphDefinition(definition, graphId, graphVersion);
      await refreshInitialRequirements().catch((err) => {
        setInitialRequirements(null);
        setInitialRequirementsError(err instanceof Error ? err.message : String(err));
      });
      const result = await startRun(initialState);
      setSelectedRunId(result.run.run_id);
      setRunStatusVisible(true);
      await refreshRuns();
      await refreshSelectedRun(result.run.run_id);
      setStatus(`run ${result.run.status}`);
    } catch (err) {
      setInitialRequirementsError(err instanceof Error ? err.message : String(err));
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function controlRun(kind: "pause" | "cancel") {
    if (!selectedRunId) return;
    setBusy(true);
    setError("");
    try {
      await (kind === "pause" ? pauseRun(selectedRunId) : cancelRun(selectedRunId));
      await refreshRuns();
      await refreshSelectedRun(selectedRunId);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function selectCheckpoint(checkpointId: string) {
    setSelectedCheckpointId(checkpointId);
    setSelectedArtifactId("");
    setArtifactDetail(null);
    setResourceStatus("loading checkpoint");
    try {
      const detail = await getCheckpoint(checkpointId);
      setCheckpointDetail(detail);
      setResourceStatus("checkpoint loaded");
    } catch (err) {
      setCheckpointDetail(null);
      setResourceStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function selectArtifact(artifact: ArtifactRef) {
    const runId = artifact.run_id || selectedRunId;
    if (!runId || !artifact.id) return;
    setSelectedArtifactId(artifact.id);
    setSelectedCheckpointId("");
    setCheckpointDetail(null);
    setResourceStatus("loading artifact");
    try {
      const detail = await getArtifact(runId, artifact.id);
      setArtifactDetail(detail);
      setResourceStatus("artifact loaded");
    } catch (err) {
      setArtifactDetail(null);
      setResourceStatus(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <WorkbenchShell
      tab={tab}
      graphInfo={graphInfo}
      selectedRun={selectedRun}
      status={status}
      busy={busy}
      definition={definition}
      runsCount={runs.length}
      error={error}
      onRefresh={loadServerState}
      onUpload={uploadGraph}
      onRun={runGraph}
      onTabChange={setTab}
      hasRunStatus={Boolean(selectedRunId)}
      runStatusVisible={runStatusVisible}
      onToggleRunStatus={() => setRunStatusVisible((visible) => !visible)}
      runStatusPanel={
        <RunStatusPanel
          run={selectedRun}
          events={displayEvents}
          steps={steps}
          onHide={() => setRunStatusVisible(false)}
        />
      }
    >
      {tab === "graph" ? (
        <GraphWorkspace
          definition={definition}
          initialStateText={initialStateText}
          initialRequirements={initialRequirements}
          initialRequirementsError={initialRequirementsError}
          steps={steps}
          events={displayEvents.slice(0, 80)}
          registry={registry}
          graphId={graphId}
          graphVersion={graphVersion}
          onGraphId={setGraphId}
          onGraphVersion={setGraphVersion}
          onDefinitionText={setDefinitionText}
          onInitialStateText={setInitialStateText}
          onLocalGraphLoaded={handleLocalGraphLoaded}
        />
      ) : null}
      {tab === "runs" ? (
        <RunsWorkspace
          runs={runs}
          selectedRunId={selectedRunId}
          steps={steps}
          onSelectRun={setSelectedRunId}
          onRefresh={refreshRuns}
          onPause={() => controlRun("pause")}
          onCancel={() => controlRun("cancel")}
          busy={busy}
        />
      ) : null}
      {tab === "observe" ? (
        <ObserveWorkspace
          selectedRunId={selectedRunId}
          streamTypes={streamTypes}
          events={displayEvents}
          onStreamTypes={setStreamTypes}
        />
      ) : null}
      {tab === "manage" ? (
        <ManageWorkspace
          checkpoints={checkpoints}
          artifacts={artifacts}
          selectedCheckpointId={selectedCheckpointId}
          selectedArtifactId={selectedArtifactId}
          checkpointDetail={checkpointDetail}
          artifactDetail={artifactDetail}
          resourceStatus={resourceStatus}
          registry={registry}
          selectedRunId={selectedRunId}
          onSelectCheckpoint={(checkpoint) => void selectCheckpoint(checkpoint.checkpoint_id)}
          onSelectArtifact={(artifact) => void selectArtifact(artifact)}
        />
      ) : null}
      {tab === "settings" ? (
        <SettingsWorkspace
          graphId={graphId}
          graphVersion={graphVersion}
          registry={registry}
          onGraphId={setGraphId}
          onGraphVersion={setGraphVersion}
        />
      ) : null}
    </WorkbenchShell>
  );
}

function runStatusFromEvent(eventType: string): string {
  switch (eventType) {
    case "run.created":
      return "pending";
    case "run.started":
    case "run.resumed":
      return "running";
    case "run.paused":
      return "paused";
    case "run.canceled":
      return "canceled";
    case "run.finished":
      return "finished";
    case "run.failed":
      return "failed";
    default:
      return "";
  }
}

function isTerminalRunStatus(status: string): boolean {
  return status === "finished" || status === "completed" || status === "failed" || status === "canceled";
}

function missingInitialStateRequirements(initialState: unknown, requirements: InitialStateRequirement[]): string[] {
  return requirements
    .filter((requirement) => !hasFilledInitialStatePath(initialState, requirement.path, requirement.type))
    .map((requirement) => requirement.path);
}

function hasFilledInitialStatePath(initialState: unknown, path: string, type?: string): boolean {
  const value = valueAtStatePath(initialState, path);
  if (!value.exists) return false;
  if (value.value === null || value.value === undefined) return false;
  if (typeof value.value === "string") return value.value.trim().length > 0;
  if ((type ?? "").toLowerCase() === "string") return typeof value.value === "string" && value.value.trim().length > 0;
  return true;
}

function valueAtStatePath(root: unknown, path: string): { exists: boolean; value?: unknown } {
  const parts = path.split(".").map((part) => part.trim()).filter(Boolean);
  let current = root;
  for (const part of parts) {
    if (!current || typeof current !== "object" || Array.isArray(current)) {
      return { exists: false };
    }
    if (!Object.prototype.hasOwnProperty.call(current, part)) {
      return { exists: false };
    }
    current = (current as Record<string, unknown>)[part];
  }
  return { exists: true, value: current };
}

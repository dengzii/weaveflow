import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  Box,
  Braces,
  Database,
  GitBranch,
  LayoutDashboard,
  Loader2,
  Pause,
  Play,
  RefreshCcw,
  Settings,
  Square,
  Upload,
} from "lucide-react";
import {
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
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { themePreferences, useTheme, type ThemePreference } from "../lib/theme";
import { cn, formatTime, parseJSON, stringifyJSON } from "../lib/utils";
import { GraphWorkspace } from "./workbench/GraphWorkspace";
import type {
  ArtifactDetail,
  ArtifactRef,
  CheckpointDetail,
  CheckpointRecord,
  GraphDefinition,
  GraphInfo,
  InitialStateRequirements,
  RegistryInfo,
  RunRecord,
  RuntimeEvent,
  StepRecord,
} from "../types";

export const workspaceTabs = ["graph", "runs", "observe", "manage", "settings"] as const;
export type WorkspaceTab = (typeof workspaceTabs)[number];

const sampleGraph: GraphDefinition = {
  version: "1.0",
  name: "debug_graph",
  entry_point: "input",
  finish_point: "input",
  nodes: [
    {
      id: "input",
      type: "human_message",
      name: "Input",
      config: { content: "hello" },
    },
  ],
};

const defaultInitialState = {
  shared: {},
  scopes: {},
  internal: {},
  runtime: {},
};

const runtimeEventTypes = [
  "run.created",
  "run.started",
  "run.pause_requested",
  "run.paused",
  "run.resumed",
  "run.cancel_requested",
  "run.canceled",
  "run.finished",
  "run.failed",
  "nodes.started",
  "nodes.finished",
  "nodes.failed",
  "nodes.retry",
  "nodes.custom",
  "llm.reasoning_chunk",
  "llm.content_chunk",
  "llm.reasoning",
  "llm.content",
  "llm.function_call",
  "llm.usage",
  "llm.call",
  "tool.started",
  "tool.called",
  "tool.returned",
  "tool.failed",
  "subgraph.started",
  "subgraph.finished",
  "subgraph.failed",
  "checkpoint.created",
  "artifact.created",
  "breakpoint.hit",
  "state.changed",
  "contract.violation",
  "warning",
];

function statusTone(status?: string): "neutral" | "ok" | "warn" | "danger" | "live" {
  switch (status) {
    case "completed":
      return "ok";
    case "running":
    case "pending":
      return "live";
    case "paused":
      return "warn";
    case "failed":
    case "canceled":
      return "danger";
    default:
      return "neutral";
  }
}

function themePreferenceLabel(preference: ThemePreference): string {
  switch (preference) {
    case "light":
      return "Light";
    case "dark":
      return "Dark";
    default:
      return "System";
  }
}

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
  const [streamTypes, setStreamTypes] = useState("");
  const [graphId, setGraphId] = useState("debug_graph");
  const [graphVersion, setGraphVersion] = useState("1.0");
  const [status, setStatus] = useState("idle");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

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

  const refreshRuns = useCallback(async () => {
    const nextRuns = await listRuns();
    setRuns(nextRuns);
    setSelectedRunId((current) => current || nextRuns.at(-1)?.run_id || "");
  }, []);

  const refreshInitialRequirements = useCallback(async () => {
    const requirements = await getInitialStateRequirements();
    setInitialRequirements(requirements);
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
        setGraphId(info.id);
        setGraphVersion(info.version);
        const def = await getGraphDefinition();
        setDefinitionText(stringifyJSON(def));
        await refreshInitialRequirements().catch(() => setInitialRequirements(null));
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
      await refreshInitialRequirements().catch(() => setInitialRequirements(null));
      await refreshRuns();
      setStatus("graph saved");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function runGraph() {
    setBusy(true);
    setError("");
    try {
      if (definition) {
        await setGraphDefinition(definition, graphId, graphVersion);
        await refreshInitialRequirements().catch(() => setInitialRequirements(null));
      }
      const result = await startRun(parseJSON<unknown>(initialStateText));
      setSelectedRunId(result.run.run_id);
      await refreshRuns();
      await refreshSelectedRun(result.run.run_id);
      setStatus(`run ${result.run.status}`);
    } catch (err) {
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

  function formatDefinition() {
    try {
      setDefinitionText(stringifyJSON(parseJSON<unknown>(definitionText)));
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <div className="flex h-screen min-h-0 bg-background text-foreground">
      <aside className="flex w-16 shrink-0 flex-col items-center border-r border-border bg-sidebar py-3">
        <div className="mb-4 flex h-9 w-9 items-center justify-center rounded-md bg-primary text-primary-foreground">
          <GitBranch className="h-4 w-4" />
        </div>
        <NavButton icon={LayoutDashboard} active={tab === "graph"} onClick={() => setTab("graph")} label="Graph" />
        <NavButton icon={Play} active={tab === "runs"} onClick={() => setTab("runs")} label="Runs" />
        <NavButton icon={Activity} active={tab === "observe"} onClick={() => setTab("observe")} label="Observe" />
        <NavButton icon={Database} active={tab === "manage"} onClick={() => setTab("manage")} label="Manage" />
        <div className="flex-1" />
        <NavButton icon={Settings} active={tab === "settings"} onClick={() => setTab("settings")} label="Settings" />
      </aside>

      <main className="grid min-w-0 flex-1 grid-rows-[auto_1fr_auto]">
        <header className="flex h-14 items-center gap-3 border-b border-border bg-background px-4">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="truncate text-sm font-semibold">WeaveFlow Debug</span>
              <Badge tone={graphInfo ? "ok" : "warn"}>{graphInfo ? graphInfo.id : "no graph"}</Badge>
              {selectedRun ? <Badge tone={statusTone(selectedRun.status)}>{selectedRun.status}</Badge> : null}
            </div>
            <div className="truncate text-xs text-muted-foreground">{status}</div>
          </div>
          <div className="flex-1" />
          <Button variant="outline" size="sm" onClick={loadServerState} disabled={busy} title="Refresh">
            <RefreshCcw className="h-4 w-4" />
            Refresh
          </Button>
          <Button variant="outline" size="sm" onClick={uploadGraph} disabled={busy || !definition} title="Upload graph">
            <Upload className="h-4 w-4" />
            Upload
          </Button>
          <Button size="sm" onClick={runGraph} disabled={busy || !definition} title="Run graph">
            {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
            Run
          </Button>
        </header>

        <section className="min-h-0">
          {tab === "graph" ? (
            <GraphWorkspace
              definition={definition}
              definitionText={definitionText}
              initialStateText={initialStateText}
              initialRequirements={initialRequirements}
              selectedRun={selectedRun}
              steps={steps}
              events={[...liveEvents, ...events].slice(0, 80)}
              registry={registry}
              graphId={graphId}
              graphVersion={graphVersion}
              onGraphId={setGraphId}
              onGraphVersion={setGraphVersion}
              onDefinitionText={setDefinitionText}
              onInitialStateText={setInitialStateText}
              onFormatDefinition={formatDefinition}
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
              events={liveEvents.length ? liveEvents : events}
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
        </section>

        <footer className="flex h-9 items-center gap-3 border-t border-border bg-muted/40 px-4 text-xs text-muted-foreground">
          <span>{definition ? `${definition.nodes.length} nodes` : "invalid graph"}</span>
          <span>{definition?.edges?.length ?? 0} edges</span>
          <span>{runs.length} runs</span>
          {error ? <span className="truncate text-destructive">{error}</span> : <span className="truncate">server API proxied at root</span>}
        </footer>
      </main>
    </div>
  );
}

function NavButton({
  icon: Icon,
  active,
  label,
  onClick,
}: {
  icon: React.ComponentType<{ className?: string }>;
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      className={cn(
        "mb-1 flex h-10 w-10 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
        active && "bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground"
      )}
      onClick={onClick}
      title={label}
      aria-label={label}
    >
      <Icon className="h-4 w-4" />
    </button>
  );
}

function RunsWorkspace({
  runs,
  selectedRunId,
  steps,
  onSelectRun,
  onRefresh,
  onPause,
  onCancel,
  busy,
}: {
  runs: RunRecord[];
  selectedRunId: string;
  steps: StepRecord[];
  onSelectRun: (id: string) => void;
  onRefresh: () => void;
  onPause: () => void;
  onCancel: () => void;
  busy: boolean;
}) {
  return (
    <div className="grid h-full min-h-0 grid-cols-[380px_minmax(0,1fr)]">
      <section className="min-h-0 border-r border-border bg-panel">
        <PanelHeader icon={Play} title="Runs" />
        <div className="flex items-center gap-2 border-b border-border p-3">
          <Button variant="outline" size="sm" onClick={() => void onRefresh()} title="Refresh runs">
            <RefreshCcw className="h-4 w-4" />
            Refresh
          </Button>
          <Button variant="outline" size="sm" onClick={onPause} disabled={busy || !selectedRunId} title="Pause run">
            <Pause className="h-4 w-4" />
            Pause
          </Button>
          <Button variant="danger" size="sm" onClick={onCancel} disabled={busy || !selectedRunId} title="Cancel run">
            <Square className="h-4 w-4" />
            Cancel
          </Button>
        </div>
        <div className="h-[calc(100%-104px)] overflow-auto">
          {runs.map((run) => (
            <button
              key={run.run_id}
              className={cn(
                "grid w-full gap-1 border-b border-border px-3 py-3 text-left hover:bg-accent",
                selectedRunId === run.run_id && "bg-accent"
              )}
              onClick={() => onSelectRun(run.run_id)}
            >
              <div className="flex items-center gap-2">
                <Badge tone={statusTone(run.status)}>{run.status}</Badge>
                <span className="truncate text-sm font-medium">{run.run_id}</span>
              </div>
              <div className="text-xs text-muted-foreground">{formatTime(run.started_at)} / {run.graph_id}</div>
            </button>
          ))}
        </div>
      </section>
      <section className="min-h-0 overflow-auto bg-background p-4">
        <PanelHeader icon={Activity} title="Steps" inline />
        <div className="mt-3 overflow-hidden rounded-md border border-border">
          <table className="w-full table-fixed text-sm">
            <thead className="bg-muted text-xs text-muted-foreground">
              <tr>
                <th className="w-28 px-3 py-2 text-left">Status</th>
                <th className="px-3 py-2 text-left">Node</th>
                <th className="w-20 px-3 py-2 text-left">Attempt</th>
                <th className="w-28 px-3 py-2 text-left">Updated</th>
              </tr>
            </thead>
            <tbody>
              {steps.map((step) => (
                <tr key={step.step_id} className="border-t border-border">
                  <td className="px-3 py-2"><Badge tone={statusTone(step.status)}>{step.status}</Badge></td>
                  <td className="truncate px-3 py-2">{step.node_name || step.node_id}</td>
                  <td className="px-3 py-2 text-muted-foreground">{step.attempt}</td>
                  <td className="px-3 py-2 text-muted-foreground">{formatTime(step.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

function ObserveWorkspace({
  selectedRunId,
  streamTypes,
  events,
  onStreamTypes,
}: {
  selectedRunId: string;
  streamTypes: string;
  events: RuntimeEvent[];
  onStreamTypes: (value: string) => void;
}) {
  return (
    <div className="grid h-full min-h-0 grid-rows-[auto_1fr] bg-background">
      <div className="flex items-center gap-3 border-b border-border p-3">
        <Badge tone={selectedRunId ? "live" : "neutral"}>{selectedRunId || "all runs"}</Badge>
        <Input
          value={streamTypes}
          onChange={(event) => onStreamTypes(event.target.value)}
          placeholder="nodes.started,nodes.finished,llm.content_chunk"
          className="max-w-lg"
        />
      </div>
      <div className="min-h-0 overflow-auto p-4">
        <EventList events={events} wide />
      </div>
    </div>
  );
}

function ManageWorkspace({
  checkpoints,
  artifacts,
  selectedCheckpointId,
  selectedArtifactId,
  checkpointDetail,
  artifactDetail,
  resourceStatus,
  registry,
  selectedRunId,
  onSelectCheckpoint,
  onSelectArtifact,
}: {
  checkpoints: CheckpointRecord[];
  artifacts: ArtifactRef[];
  selectedCheckpointId: string;
  selectedArtifactId: string;
  checkpointDetail: CheckpointDetail | null;
  artifactDetail: ArtifactDetail | null;
  resourceStatus: string;
  registry: RegistryInfo | null;
  selectedRunId: string;
  onSelectCheckpoint: (checkpoint: CheckpointRecord) => void;
  onSelectArtifact: (artifact: ArtifactRef) => void;
}) {
  return (
    <div className="grid h-full min-h-0 grid-cols-[300px_300px_minmax(0,1fr)] bg-background">
      <ResourceColumn title="Checkpoints" icon={Database}>
        <ResourceList
          items={checkpoints.map((item) => ({
            id: item.checkpoint_id,
            meta: `${item.stage} / ${item.node_id}`,
            source: item,
          }))}
          selectedId={selectedCheckpointId}
          onSelect={(item) => onSelectCheckpoint(item.source)}
          empty={selectedRunId ? "No checkpoints" : "No run selected"}
        />
      </ResourceColumn>
      <ResourceColumn title="Artifacts" icon={Box}>
        <ResourceList
          items={artifacts.map((item) => ({
            id: item.id,
            meta: `${item.type || "artifact"} / ${item.mime_type || ""}`,
            source: item,
          }))}
          selectedId={selectedArtifactId}
          onSelect={(item) => onSelectArtifact(item.source)}
          empty={selectedRunId ? "No artifacts" : "No run selected"}
        />
      </ResourceColumn>
      <ResourceColumn title="Detail" icon={Braces}>
        <div className="grid gap-3">
          {resourceStatus ? <div className="text-xs text-muted-foreground">{resourceStatus}</div> : null}
          <ResourceDetail checkpoint={checkpointDetail} artifact={artifactDetail} />
          <div className="rounded-md border border-border bg-panel p-3">
            <div className="mb-2 text-sm font-medium">Registry Snapshot</div>
            <ResourceList
              items={(registry?.node_types ?? []).slice(0, 12).map((item) => ({
                id: item.type,
                meta: item.title || "node type",
                source: item,
              }))}
              empty="Registry unavailable"
            />
          </div>
        </div>
      </ResourceColumn>
    </div>
  );
}

function SettingsWorkspace({
  graphId,
  graphVersion,
  registry,
  onGraphId,
  onGraphVersion,
}: {
  graphId: string;
  graphVersion: string;
  registry: RegistryInfo | null;
  onGraphId: (value: string) => void;
  onGraphVersion: (value: string) => void;
}) {
  const { preference, resolvedTheme, setPreference } = useTheme();

  return (
    <div className="grid h-full min-h-0 grid-cols-[420px_minmax(0,1fr)] bg-background">
      <section className="border-r border-border bg-panel p-4">
        <PanelHeader icon={Settings} title="Settings" inline />
        <div className="mt-4 grid gap-4">
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Graph ID</span>
            <Input value={graphId} onChange={(event) => onGraphId(event.target.value)} />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Graph Version</span>
            <Input value={graphVersion} onChange={(event) => onGraphVersion(event.target.value)} />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="text-xs font-medium text-muted-foreground">Theme</span>
            <Select
              value={preference}
              onChange={(event) => setPreference(event.target.value as ThemePreference)}
            >
              {themePreferences.map((item) => (
                <option key={item} value={item}>
                  {themePreferenceLabel(item)}
                </option>
              ))}
            </Select>
            <span className="text-xs text-muted-foreground">Resolved: {resolvedTheme}</span>
          </label>
        </div>
      </section>
      <section className="min-h-0 overflow-auto p-4">
        <PanelHeader icon={Braces} title="Extension Points" inline />
        <div className="mt-4 grid grid-cols-2 gap-3">
          {["Node palette", "Condition builder", "Runner profiles", "Artifact viewers", "Schema forms", "Layout plugins"].map((item) => (
            <div key={item} className="rounded-md border border-dashed border-border bg-panel p-3">
              <div className="text-sm font-medium">{item}</div>
              <div className="mt-1 text-xs text-muted-foreground">Reserved</div>
            </div>
          ))}
        </div>
        <div className="mt-4 rounded-md border border-border bg-panel p-3">
          <div className="mb-2 text-sm font-medium">Registry Snapshot</div>
          <pre className="max-h-80 overflow-auto rounded bg-muted p-3 text-xs">
            {stringifyJSON({
              state_fields: registry?.state_fields?.length ?? 0,
              node_types: registry?.node_types?.map((node) => node.type) ?? [],
              conditions: registry?.conditions?.map((condition) => condition.type) ?? [],
            })}
          </pre>
        </div>
      </section>
    </div>
  );
}

function PanelHeader({
  icon: Icon,
  title,
  inline = false,
}: {
  icon: React.ComponentType<{ className?: string }>;
  title: string;
  inline?: boolean;
}) {
  return (
    <div className={cn("flex h-12 items-center gap-2 px-3", !inline && "border-b border-border")}>
      <Icon className="h-4 w-4 text-muted-foreground" />
      <span className="text-sm font-semibold">{title}</span>
    </div>
  );
}

function InfoRows({ rows }: { rows: Array<[string, string]> }) {
  return (
    <div className="grid gap-1">
      {rows.map(([label, value]) => (
        <div key={label} className="grid grid-cols-[84px_minmax(0,1fr)] gap-2 text-xs">
          <span className="text-muted-foreground">{label}</span>
          <span className="truncate font-mono">{value || "-"}</span>
        </div>
      ))}
    </div>
  );
}

function EventList({ events, wide = false }: { events: RuntimeEvent[]; wide?: boolean }) {
  if (events.length === 0) {
    return <div className="text-sm text-muted-foreground">No events</div>;
  }
  return (
    <div className="grid gap-2">
      {events.map((event, index) => (
        <div key={`${event.id}-${index}`} className="rounded-md border border-border bg-background p-2">
          <div className="flex min-w-0 items-center gap-2">
            <Badge tone={event.type.includes("failed") ? "danger" : event.type.includes("finished") ? "ok" : "neutral"}>
              {event.type}
            </Badge>
            <span className="truncate text-xs text-muted-foreground">{event.node_id || event.run_id}</span>
            <span className="ml-auto text-xs text-muted-foreground">{formatTime(event.timestamp)}</span>
          </div>
          {wide && event.payload ? (
            <pre className="mt-2 max-h-48 overflow-auto rounded bg-muted p-2 text-xs">{stringifyJSON(event.payload)}</pre>
          ) : null}
        </div>
      ))}
    </div>
  );
}

function ResourceDetail({
  checkpoint,
  artifact,
}: {
  checkpoint: CheckpointDetail | null;
  artifact: ArtifactDetail | null;
}) {
  if (artifact) {
    const preview = artifact.text ?? artifact.data_base64 ?? "";
    return (
      <div className="rounded-md border border-border bg-panel p-3">
        <div className="mb-3 flex items-center gap-2">
          <Badge>{artifact.artifact.type || "artifact"}</Badge>
          <span className="truncate text-sm font-medium">{artifact.artifact.id}</span>
        </div>
        <InfoRows
          rows={[
            ["node", artifact.artifact.node_id || ""],
            ["step", artifact.artifact.step_id || ""],
            ["mime", artifact.artifact.mime_type || ""],
            ["size", String(artifact.size)],
          ]}
        />
        <pre className="mt-3 max-h-[420px] overflow-auto rounded bg-muted p-3 text-xs">
          {preview || stringifyJSON({ artifact: artifact.artifact, size: artifact.size })}
        </pre>
      </div>
    );
  }

  if (checkpoint) {
    return (
      <div className="rounded-md border border-border bg-panel p-3">
        <div className="mb-3 flex items-center gap-2">
          <Badge>{checkpoint.record.stage}</Badge>
          <span className="truncate text-sm font-medium">{checkpoint.record.checkpoint_id}</span>
        </div>
        <InfoRows
          rows={[
            ["node", checkpoint.record.node_id],
            ["step", checkpoint.record.step_id],
            ["codec", checkpoint.record.state_codec],
            ["created", formatTime(checkpoint.record.created_at)],
          ]}
        />
        <pre className="mt-3 max-h-[420px] overflow-auto rounded bg-muted p-3 text-xs">
          {stringifyJSON({
            runtime: checkpoint.runtime ?? null,
            business: checkpoint.business ?? null,
            artifacts: checkpoint.artifacts ?? [],
            snapshot: checkpoint.snapshot ?? null,
          })}
        </pre>
      </div>
    );
  }

  return <div className="rounded-md border border-border bg-panel p-3 text-sm text-muted-foreground">Select a checkpoint or artifact</div>;
}

function ResourceColumn({
  title,
  icon,
  children,
}: {
  title: string;
  icon: React.ComponentType<{ className?: string }>;
  children: React.ReactNode;
}) {
  return (
    <section className="min-h-0 overflow-auto border-r border-border p-4 last:border-r-0">
      <PanelHeader icon={icon} title={title} inline />
      <div className="mt-3">{children}</div>
    </section>
  );
}

function ResourceList<T>({
  items,
  empty,
  selectedId,
  onSelect,
}: {
  items: Array<{ id: string; meta: string; source: T }>;
  empty: string;
  selectedId?: string;
  onSelect?: (item: { id: string; meta: string; source: T }) => void;
}) {
  if (items.length === 0) {
    return <div className="text-sm text-muted-foreground">{empty}</div>;
  }
  return (
    <div className="grid gap-2">
      {items.map((item) => (
        <button
          key={item.id}
          type="button"
          onClick={() => onSelect?.(item)}
          className={cn(
            "min-w-0 rounded-md border border-border bg-panel p-3 text-left hover:bg-accent",
            selectedId === item.id && "border-primary bg-accent"
          )}
        >
          <div className="truncate text-sm font-medium">{item.id}</div>
          <div className="mt-1 truncate text-xs text-muted-foreground">{item.meta}</div>
        </button>
      ))}
    </div>
  );
}

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  analyzeInitialStateRequirements,
  cancelRun,
  deleteRun,
  getGraphDefinition,
  getGraphInfo,
  getInitialStateRequirements,
  getRegistry,
  getGraphSettings,
  getCheckpoint,
  getTools,
  listRuns,
  getRunDetail,
  pauseRun,
  resumeRun,
  setGraphDefinition,
  startRun,
  updateGraphSettings,
} from "../api";
import { Button } from "../components/ui/button";
import { Textarea } from "../components/ui/textarea";
import { parseJSON, stringifyJSON } from "../lib/utils";
import { pickInitialLocalGraphDraft, readLocalGraphDrafts } from "../lib/localGraphs";
import { emitRuntimeEvent } from "../lib/runtimeEvents";
import {
  defaultInitialState,
  runtimeEventTypes,
  sampleGraph,
  workspaceTabs,
  type WorkspaceTab,
} from "./workbench/constants";
import { GraphWorkspace } from "./workbench/GraphWorkspace";
import { RegistryDialog } from "./workbench/RegistryDialog";
import { RunStatusPanel } from "./workbench/RunStatusPanel";
import { SettingsWorkspace } from "./workbench/SettingsWorkspace";
import { TriggerWorkspace } from "./workbench/TriggerWorkspace";
import { WorkbenchShell } from "./workbench/WorkbenchShell";
import type { ToastRecord, ToastTone } from "./workbench/graph-workspace/ToastStack";
import { validateGraph } from "./workbench/graph-workspace/utils";
import type {
  GraphDefinition,
  GraphInfo,
  GraphSettings,
  GraphSettingsUpdate,
  InitialStateRequirement,
  InitialStateRequirements,
  CheckpointRecord,
  RegistryInfo,
  RunInterrupt,
  RunRecord,
  RuntimeEvent,
  StepRecord,
  ToolDefinition,
} from "../types";

export { workspaceTabs };
export type { WorkspaceTab };

type StreamStatus = "connecting" | "connected" | "reconnecting" | "closed";
type GraphIdentity = { id: string; version: string };
const RUN_STATUS_VISIBLE_STORAGE_KEY = "weaveflow.workbench.runStatus.visible";

interface HumanMessagePrompt {
  runId: string;
  checkpointId: string;
  nodeId: string;
  statePath: string;
  message: string;
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
  const [toolDefinitions, setToolDefinitions] = useState<ToolDefinition[]>([]);
  const [graphSettings, setGraphSettings] = useState<GraphSettings | null>(null);
  const [runs, setRuns] = useState<RunRecord[]>([]);
  const [selectedRunId, setSelectedRunId] = useState("");
  const [steps, setSteps] = useState<StepRecord[]>([]);
  const [events, setEvents] = useState<RuntimeEvent[]>([]);
  const [runInterrupt, setRunInterrupt] = useState<RunInterrupt | null>(null);
  const [runState, setRunState] = useState<unknown>(null);
  const [humanPrompt, setHumanPrompt] = useState<HumanMessagePrompt | null>(null);
  const [humanPromptText, setHumanPromptText] = useState("");
  const [liveEvents, setLiveEvents] = useState<RuntimeEvent[]>([]);
  const [runStatusVisible, setRunStatusVisible] = useState(readStoredRunStatusVisible);
  const [registryDialogOpen, setRegistryDialogOpen] = useState(false);
  const [graphId, setGraphId] = useState("debug_graph");
  const [graphVersion, setGraphVersion] = useState("1.0");
  const [initialRequirementsError, setInitialRequirementsError] = useState("");
  const [toasts, setToasts] = useState<ToastRecord[]>([]);
  const [streamStatus, setStreamStatus] = useState<StreamStatus>("connecting");
  const [busy, setBusy] = useState(false);
  const [runLaunchPending, setRunLaunchPending] = useState(false);
  const preferLocalGraphRef = useRef(false);
  const checkedLocalGraphRef = useRef(false);
  const toastSeqRef = useRef(0);
  const selectedRunIdRef = useRef("");
  const launchPendingRef = useRef(false);
  const launchRunIdRef = useRef("");
  const graphIdentityRef = useRef({ id: graphId, version: graphVersion });
  const graphRunsRefreshStartedRef = useRef(false);
  const runContextVersionRef = useRef(0);
  const ignoredHumanInterruptsRef = useRef<Set<string>>(new Set());
  const humanPromptCheckpointRef = useRef("");

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
  const currentGraphIdentity = useMemo(
    () => ({
      id: graphId || graphInfo?.id || "graph",
      version: graphVersion || graphInfo?.version || "1.0",
    }),
    [graphId, graphInfo?.id, graphInfo?.version, graphVersion]
  );
  const canResumeSelectedRun = canResumeRun(selectedRun, runInterrupt);
  const headerRunControlMode = runControlModeFromRun(selectedRun, runInterrupt);
  const graphSwitchLocked =
    runLaunchPending ||
    Boolean(selectedRun && isActiveRunStatus(selectedRun.status)) ||
    runs.some((run) => isActiveRunStatus(run.status) && matchesGraphIdentity(run, currentGraphIdentity));
  const displayEvents = useMemo(() => {
    const seen = new Set<string>();
    const targetRunId = selectedRunId;
    return [...liveEvents, ...events]
      .filter((event) => {
        if (targetRunId && event.run_id !== targetRunId) return false;
        const key = event.id || `${event.run_id}:${event.type}:${event.timestamp}:${event.node_id ?? ""}`;
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
  }, [events, liveEvents, selectedRunId]);

  useEffect(() => {
    selectedRunIdRef.current = selectedRunId;
  }, [selectedRunId]);

  useEffect(() => {
    writeStoredRunStatusVisible(runStatusVisible);
  }, [runStatusVisible]);

  useEffect(() => {
    graphIdentityRef.current = currentGraphIdentity;
  }, [currentGraphIdentity]);

  const pushToast = useCallback((tone: ToastTone, message: string) => {
    const trimmed = message.trim();
    if (!trimmed) return;
    const id = `toast-${Date.now()}-${toastSeqRef.current++}`;
    setToasts((current) => [...current, { id, tone, message: trimmed }].slice(-5));
  }, []);

  const dismissToast = useCallback((id: string) => {
    setToasts((current) => current.filter((toast) => toast.id !== id));
  }, []);

  const notifyError = useCallback(
    (err: unknown): string => {
      const message = err instanceof Error ? err.message : String(err);
      pushToast("error", message);
      return message;
    },
    [pushToast]
  );

  const resetRunState = useCallback(() => {
    runContextVersionRef.current += 1;
    launchPendingRef.current = false;
    launchRunIdRef.current = "";
    selectedRunIdRef.current = "";
    ignoredHumanInterruptsRef.current.clear();
    humanPromptCheckpointRef.current = "";
    setRunLaunchPending(false);
    setRuns([]);
    setSelectedRunId("");
    setSteps([]);
    setEvents([]);
    setLiveEvents([]);
    setRunInterrupt(null);
    setRunState(null);
    setHumanPrompt(null);
    setHumanPromptText("");
  }, []);

  const maybeOpenHumanPrompt = useCallback(
    (interrupt?: RunInterrupt | null) => {
      const prompt = humanMessagePromptFromInterrupt(interrupt, definition);
      if (!prompt) return;
      if (ignoredHumanInterruptsRef.current.has(prompt.checkpointId)) return;
      if (humanPromptCheckpointRef.current === prompt.checkpointId) return;
      humanPromptCheckpointRef.current = prompt.checkpointId;
      setHumanPrompt(prompt);
      setHumanPromptText("");
    },
    [definition]
  );

  useEffect(() => {
    maybeOpenHumanPrompt(runInterrupt);
  }, [maybeOpenHumanPrompt, runInterrupt]);

  const refreshRuns = useCallback(async (
    identity: GraphIdentity = graphIdentityRef.current,
    contextVersion = runContextVersionRef.current,
    autoSelect = true
  ) => {
    const nextRuns = (await listRuns(identity.id)) ?? [];
    if (runContextVersionRef.current !== contextVersion) return;
    setRuns(nextRuns);
    if (autoSelect) {
      setSelectedRunId((current) => {
        if (current && nextRuns.some((run) => run.run_id === current)) return current;
        return nextRuns.at(-1)?.run_id || "";
      });
    }
  }, []);

  useEffect(() => {
    if (!graphRunsRefreshStartedRef.current) {
      graphRunsRefreshStartedRef.current = true;
      return;
    }
    const contextVersion = runContextVersionRef.current;
    void refreshRuns(currentGraphIdentity, contextVersion).catch((err) => {
      notifyError(err);
    });
  }, [currentGraphIdentity.id, notifyError, refreshRuns]);

  const refreshInitialRequirements = useCallback(async () => {
    const requirements = await getInitialStateRequirements();
    setInitialRequirements(requirements);
    setInitialRequirementsError("");
    return requirements;
  }, []);

  const refreshSelectedRun = useCallback(
    async (runId: string) => {
      const contextVersion = runContextVersionRef.current;
      if (!runId) {
        setSteps([]);
        setEvents([]);
        setRunInterrupt(null);
        setRunState(null);
        setHumanPrompt(null);
        setHumanPromptText("");
        humanPromptCheckpointRef.current = "";
        return;
      }
      const detail = await getRunDetail(runId, graphIdentityRef.current.id);
      const state = await loadLatestRunState(detail.checkpoints, graphIdentityRef.current.id);
      if (runContextVersionRef.current !== contextVersion) return;
      setSteps(detail.steps);
      setEvents(detail.events);
      setRunInterrupt(detail.interrupt ?? null);
      setRunState(state);
      setRuns((current) => current.map((run) => (run.run_id === detail.run.run_id ? detail.run : run)));
    },
    []
  );

  const refreshPausedRun = useCallback(
    async (runId: string, options: { openHumanPrompt?: boolean } = {}) => {
      if (!runId) return;
      const contextVersion = runContextVersionRef.current;
      try {
        const detail = await getRunDetail(runId, graphIdentityRef.current.id);
        const state = await loadLatestRunState(detail.checkpoints, graphIdentityRef.current.id);
        if (runContextVersionRef.current !== contextVersion) return;
        setRuns((current) => {
          const exists = current.some((run) => run.run_id === detail.run.run_id);
          if (exists) return current.map((run) => (run.run_id === detail.run.run_id ? detail.run : run));
          return [...current, detail.run];
        });

        const selectedRun = selectedRunIdRef.current;
        const launchRun = launchRunIdRef.current;
        if (selectedRun && selectedRun !== runId && launchRun !== runId) {
          return;
        }

        setSelectedRunId(runId);
        setRunStatusVisible(true);
        setSteps(detail.steps);
        setEvents(detail.events);
        setRunInterrupt(detail.interrupt ?? null);
        setRunState(state);
        if (options.openHumanPrompt) maybeOpenHumanPrompt(detail.interrupt ?? null);
      } catch (err) {
        notifyError(err);
      }
    },
    [maybeOpenHumanPrompt, notifyError]
  );

  const loadServerState = useCallback(async () => {
    try {
      const [info, reg, tools, settings] = await Promise.all([
        getGraphInfo().catch(() => null),
        getRegistry().catch(() => null),
        getTools().catch(() => null),
        getGraphSettings().catch(() => null),
      ]);
      setGraphInfo(info);
      setRegistry(reg);
      setToolDefinitions(tools?.tools ?? []);
      setGraphSettings(settings);
      if (info) {
        if (!preferLocalGraphRef.current) {
          setGraphId(info.id);
          setGraphVersion(info.version);
          graphIdentityRef.current = { id: info.id || "graph", version: info.version || "1.0" };
          const def = await getGraphDefinition();
          setDefinitionText(stringifyJSON(def));
          await refreshInitialRequirements().catch((err) => {
            setInitialRequirements(null);
            setInitialRequirementsError(err instanceof Error ? err.message : String(err));
          });
        }
      }
      const loadIdentity = info
        ? { id: info.id || "graph", version: info.version || "1.0" }
        : graphIdentityRef.current;
      await refreshRuns(loadIdentity, undefined, !preferLocalGraphRef.current).catch(() => undefined);
    } catch (err) {
      notifyError(err);
    }
  }, [notifyError, refreshInitialRequirements, refreshRuns]);

  useEffect(() => {
    void loadServerState();
  }, [loadServerState]);

  const handleLocalGraphLoaded = useCallback(() => {
    preferLocalGraphRef.current = true;
  }, []);

  const prepareGraphSwitch = useCallback(() => {
    if (graphSwitchLocked) {
      pushToast("warn", "Cannot switch graph while a run is active");
      return false;
    }
    preferLocalGraphRef.current = true;
    resetRunState();
    return true;
  }, [graphSwitchLocked, pushToast, resetRunState]);

  useEffect(() => {
    if (!definition || validateGraph(definition, registry)) {
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
  }, [definition, graphId, graphVersion, registry]);

  useEffect(() => {
    void refreshSelectedRun(selectedRunId).catch((err) => {
      notifyError(err);
    });
  }, [notifyError, refreshSelectedRun, selectedRunId]);

  useEffect(() => {
    const streamPath = "/events/stream";
    let source: EventSource | null = null;
    let reconnectTimer = 0;
    let closed = false;

    const onEvent = (message: MessageEvent<string>) => {
      try {
        const event = JSON.parse(message.data) as RuntimeEvent;
        const selectedRun = selectedRunIdRef.current;
        const launchRun = launchRunIdRef.current;
        const launchMatches = launchPendingRef.current && (!launchRun || event.run_id === launchRun);
        const shouldTrack =
          !event.run_id ||
          (selectedRun
            ? event.run_id === selectedRun || event.run_id === launchRun
            : launchMatches);
        if (!shouldTrack) return;

        emitRuntimeEvent(event);
        setLiveEvents((current) => {
          const retainRun = event.run_id || selectedRun || launchRun;
          const next = [event, ...current];
          if (!retainRun) return next;
          return next.filter((item) => !item.run_id || item.run_id === retainRun);
        });
        const nextStatus = runStatusFromEvent(event.type);
        if (event.run_id) {
          setRuns((current) =>
            upsertRunFromEvent(current, event, nextStatus, graphIdentityRef.current)
          );
        }
        if (event.run_id && (event.type === "run.created" || (launchPendingRef.current && !launchRunIdRef.current))) {
          launchRunIdRef.current = event.run_id;
          if (launchPendingRef.current || !selectedRunIdRef.current) {
            setSelectedRunId(event.run_id);
            setRunStatusVisible(true);
          }
        }
        if (event.run_id && event.run_id === launchRunIdRef.current && nextStatus && isTerminalRunStatus(nextStatus)) {
          launchPendingRef.current = false;
          setRunLaunchPending(false);
        }
        if (event.run_id && event.type === "run.paused") {
          void refreshPausedRun(event.run_id, { openHumanPrompt: true });
        }
      } catch {
        // ignore malformed frames
      }
    };

    const connect = () => {
      if (closed) return;
      setStreamStatus((current) => (current === "reconnecting" ? "reconnecting" : "connecting"));
      source = new EventSource(streamPath);
      source.onopen = () => {
        setStreamStatus("connected");
      };
      source.onmessage = onEvent;
      for (const eventType of runtimeEventTypes) {
        source.addEventListener(eventType, onEvent as EventListener);
      }
      source.onerror = () => {
        if (closed) return;
        setStreamStatus("reconnecting");
        source?.close();
        reconnectTimer = window.setTimeout(connect, 1500);
      };
    };

    connect();
    return () => {
      closed = true;
      window.clearTimeout(reconnectTimer);
      if (source) {
        for (const eventType of runtimeEventTypes) {
          source.removeEventListener(eventType, onEvent as EventListener);
        }
        source.close();
      }
      setStreamStatus("closed");
    };
  }, [refreshPausedRun]);

  async function runGraph() {
    setBusy(true);
    try {
      if (!definition) {
        pushToast("error", "Graph JSON is invalid");
        return;
      }
      const graphValidationError = validateGraph(definition, registry);
      if (graphValidationError) {
        pushToast("error", `Graph validation failed: ${graphValidationError}`);
        setTab("graph");
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
        pushToast("error", `Missing initial state: ${preview}${suffix}`);
        setTab("graph");
        return;
      }
      if (requirements.unresolved.length > 0) {
        const unresolved = requirements.unresolved.map((item) => item.path).slice(0, 4).join(", ");
        const suffix = requirements.unresolved.length > 4 ? ` (+${requirements.unresolved.length - 4} more)` : "";
        pushToast("error", `Unresolved state requirements: ${unresolved}${suffix}`);
        setTab("graph");
        return;
      }

      await setGraphDefinition(definition, graphId, graphVersion);
      await refreshInitialRequirements().catch((err) => {
        setInitialRequirements(null);
        setInitialRequirementsError(err instanceof Error ? err.message : String(err));
      });
      const runContextVersion = ++runContextVersionRef.current;
      launchPendingRef.current = true;
      setRunLaunchPending(true);
      launchRunIdRef.current = "";
      setSelectedRunId("");
      setLiveEvents([]);
      setEvents([]);
      setRunInterrupt(null);
      setHumanPrompt(null);
      setHumanPromptText("");
      humanPromptCheckpointRef.current = "";
      ignoredHumanInterruptsRef.current.clear();
      setSteps([]);
      setRunStatusVisible(true);
      const result = await startRun(initialState);
      launchPendingRef.current = false;
      setRunLaunchPending(false);
      setSelectedRunId(result.run.run_id);
      setRunInterrupt(result.interrupt ?? null);
      setRunStatusVisible(true);
      await refreshRuns(graphIdentityRef.current, runContextVersion);
      await refreshSelectedRun(result.run.run_id);
      pushToast("info", `Run ${result.run.status}: ${result.run.run_id}`);
    } catch (err) {
      launchPendingRef.current = false;
      setRunLaunchPending(false);
      setInitialRequirementsError(err instanceof Error ? err.message : String(err));
      notifyError(err);
    } finally {
      setBusy(false);
    }
  }

  async function controlRun(kind: "pause" | "cancel") {
    if (!selectedRunId) return;
    setBusy(true);
    try {
      const run = await (kind === "pause"
        ? pauseRun(selectedRunId)
        : cancelRun(selectedRunId, selectedRun?.graph_id || graphIdentityRef.current.id));
      setRuns((current) => current.map((item) => (item.run_id === run.run_id ? run : item)));
      setSelectedRunId(run.run_id);
      await refreshRuns(graphIdentityRef.current, runContextVersionRef.current);
      await refreshSelectedRun(run.run_id);
      pushToast(kind === "pause" ? "warn" : "info", `${kind === "pause" ? "Run paused" : "Run stopped"}: ${run.run_id}`);
    } catch (err) {
      notifyError(err);
    } finally {
      setBusy(false);
    }
  }

  async function deleteSelectedRun(runId: string) {
    const target = runs.find((item) => item.run_id === runId) ?? selectedRun;
    if (!target || isActiveRunStatus(target.status)) return;
    if (!window.confirm(`Delete run ${runId}?`)) return;

    setBusy(true);
    try {
      const graphID = target.graph_id || graphIdentityRef.current.id;
      await deleteRun(runId, graphID);
      const remainingRuns = runs.filter((item) => item.run_id !== runId);
      const nextRunId = remainingRuns.at(-1)?.run_id || "";
      setRuns(remainingRuns);
      setSelectedRunId(nextRunId);
      if (selectedRunIdRef.current === runId) {
        setLiveEvents([]);
        setEvents([]);
        setSteps([]);
        setRunInterrupt(null);
        setHumanPrompt(null);
        setHumanPromptText("");
        humanPromptCheckpointRef.current = "";
      }
      if (launchRunIdRef.current === runId) {
        launchRunIdRef.current = "";
      }
      await refreshRuns(graphIdentityRef.current, runContextVersionRef.current, false);
      if (nextRunId) {
        await refreshSelectedRun(nextRunId);
      }
      pushToast("info", `Run deleted: ${runId}`);
    } catch (err) {
      notifyError(err);
    } finally {
      setBusy(false);
    }
  }

  async function resumeSelectedRun() {
    if (!selectedRunId) return;
    const runId = selectedRunId;
    const humanPromptTarget = humanMessagePromptFromInterrupt(runInterrupt, definition);
    if (humanPromptTarget) {
      humanPromptCheckpointRef.current = humanPromptTarget.checkpointId;
      setHumanPrompt(humanPromptTarget);
      setHumanPromptText("");
      setRunStatusVisible(true);
      pushToast("warn", "Human input required to resume");
      return;
    }
    setBusy(true);
    try {
      const input = parseJSON<unknown>(initialStateText);
      const runContextVersion = runContextVersionRef.current;
      setRuns((current) =>
        current.map((run) =>
          run.run_id === runId
            ? { ...run, status: "running", pause_requested: false, updated_at: new Date().toISOString() }
            : run
        )
      );
      setRunInterrupt(null);
      const result = await resumeRun(runId, input);
      setSelectedRunId(result.run.run_id);
      setRunInterrupt(result.interrupt ?? null);
      setRunStatusVisible(true);
      await refreshRuns(graphIdentityRef.current, runContextVersion);
      await refreshSelectedRun(result.run.run_id);
      pushToast("info", `Run ${result.run.status}: ${result.run.run_id}`);
    } catch (err) {
      notifyError(err);
    } finally {
      setBusy(false);
    }
  }

  async function submitHumanMessagePrompt() {
    if (!humanPrompt) return;
    const prompt = humanPrompt;
    const text = humanPromptText.trim();
    if (!text) return;
    ignoredHumanInterruptsRef.current.add(prompt.checkpointId);
    setHumanPrompt(null);
    setHumanPromptText("");
    setRunInterrupt(null);
    pushToast("info", "Human input submitted");
    void (async () => {
      try {
        const runContextVersion = runContextVersionRef.current;
        setRuns((current) =>
          current.map((run) =>
            run.run_id === prompt.runId
              ? { ...run, status: "running", pause_requested: false, updated_at: new Date().toISOString() }
              : run
          )
        );
        const result = await resumeRun(prompt.runId, pendingHumanInputState(prompt.statePath, text));
        setSelectedRunId(result.run.run_id);
        setRunInterrupt(result.interrupt ?? null);
        setRunStatusVisible(true);
        await refreshRuns(graphIdentityRef.current, runContextVersion);
        await refreshSelectedRun(result.run.run_id);
        pushToast("info", `Run ${result.run.status}: ${result.run.run_id}`);
      } catch (err) {
        ignoredHumanInterruptsRef.current.delete(prompt.checkpointId);
        humanPromptCheckpointRef.current = "";
        notifyError(err);
      }
    })();
  }

  function dismissHumanMessagePrompt() {
    if (humanPrompt?.checkpointId) {
      ignoredHumanInterruptsRef.current.add(humanPrompt.checkpointId);
    }
    setHumanPrompt(null);
    setHumanPromptText("");
    humanPromptCheckpointRef.current = "";
  }

  async function saveGraphSettings(settings: GraphSettingsUpdate): Promise<GraphSettings> {
    try {
      const next = await updateGraphSettings(settings);
      setGraphSettings(next);
      pushToast("info", "Graph settings updated");
      return next;
    } catch (err) {
      notifyError(err);
      throw err;
    }
  }

  return (
    <WorkbenchShell
      tab={tab}
      streamStatus={streamStatus}
      busy={busy}
      definition={definition}
      runControlMode={headerRunControlMode}
      canResume={canResumeSelectedRun}
      onRun={runGraph}
      onPause={() => controlRun("pause")}
      onStop={() => controlRun("cancel")}
      onResume={() => void resumeSelectedRun()}
      onShowRegistry={() => setRegistryDialogOpen(true)}
      onTabChange={setTab}
      hasRunStatus={runs.length > 0 || Boolean(selectedRunId)}
      runStatusVisible={runStatusVisible}
      onToggleRunStatus={() => setRunStatusVisible((visible) => !visible)}
      runStatusPanel={
        <RunStatusPanel
          run={selectedRun}
          runs={runs}
          selectedRunId={selectedRunId}
          onSelectRun={(runId) => {
            setSelectedRunId(runId);
            setRunStatusVisible(true);
          }}
          onDeleteRun={(runId) => void deleteSelectedRun(runId)}
          events={displayEvents}
          steps={steps}
          stateSnapshot={runState}
          definition={definition}
          registry={registry}
          onHide={() => setRunStatusVisible(false)}
        />
      }
    >
      {tab === "graph" ? (
        <GraphWorkspace
          definition={definition}
          definitionText={definitionText}
          initialStateText={initialStateText}
          initialRequirements={initialRequirements}
          initialRequirementsError={initialRequirementsError}
          steps={steps}
          selectedRunId={selectedRunId}
          registry={registry}
          toolDefinitions={toolDefinitions}
          graphSettings={graphSettings}
          onUpdateGraphSettings={saveGraphSettings}
          graphId={graphId}
          graphVersion={graphVersion}
          graphSwitchDisabled={graphSwitchLocked}
          toasts={toasts}
          onGraphId={setGraphId}
          onGraphVersion={setGraphVersion}
          onDefinitionText={setDefinitionText}
          onInitialStateText={setInitialStateText}
          onDismissToast={dismissToast}
          onGraphSwitch={prepareGraphSwitch}
          onLocalGraphLoaded={handleLocalGraphLoaded}
        />
      ) : null}
      {tab === "settings" ? (
        <SettingsWorkspace registry={registry} />
      ) : null}
      {tab === "triggers" ? <TriggerWorkspace /> : null}
      <HumanMessagePromptDialog
        prompt={humanPrompt}
        value={humanPromptText}
        busy={busy}
        onChange={setHumanPromptText}
        onCancel={dismissHumanMessagePrompt}
        onSubmit={() => void submitHumanMessagePrompt()}
      />
      <RegistryDialog
        open={registryDialogOpen}
        registry={registry}
        toolDefinitions={toolDefinitions}
        onClose={() => setRegistryDialogOpen(false)}
      />
    </WorkbenchShell>
  );
}

function HumanMessagePromptDialog({
  prompt,
  value,
  busy,
  onChange,
  onCancel,
  onSubmit,
}: {
  prompt: HumanMessagePrompt | null;
  value: string;
  busy: boolean;
  onChange: (value: string) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  if (!prompt) return null;
  const canSubmit = value.trim().length > 0 && !busy;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 p-4">
      <div className="w-[min(520px,100%)] rounded-md border border-border bg-panel shadow-xl">
        <div className="border-b border-border px-4 py-3">
          <div className="text-sm font-semibold">Human input required</div>
        </div>
        <div className="p-4">
          <Textarea
            value={value}
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={(event) => {
              if ((event.ctrlKey || event.metaKey) && event.key === "Enter" && canSubmit) {
                event.preventDefault();
                onSubmit();
              }
            }}
            autoFocus
            aria-label="Human response"
            placeholder={prompt.message || "The run is waiting for human input."}
            className="min-h-28"
          />
        </div>
        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            Dismiss
          </Button>
          <Button onClick={onSubmit} disabled={!canSubmit}>
            Resume run
          </Button>
        </div>
      </div>
    </div>
  );
}

async function loadLatestRunState(checkpoints: CheckpointRecord[], graphId: string): Promise<unknown> {
  const latest = [...checkpoints].sort(
    (left, right) => Date.parse(right.created_at) - Date.parse(left.created_at)
  )[0];
  if (!latest) return null;
  const checkpoint = await getCheckpoint(latest.checkpoint_id, graphId);
  return checkpoint.business ?? checkpoint.snapshot ?? null;
}

export function humanMessagePromptFromInterrupt(
  interrupt: RunInterrupt | null | undefined,
  definition: GraphDefinition | null
): HumanMessagePrompt | null {
  if (!interrupt?.run_id || !interrupt.checkpoint_id || !interrupt.node_id || !definition) return null;
  const node = definition.nodes.find((item) => item.id === interrupt.node_id);
  if (node?.type !== "conversation_input") return null;
  const config = isRecord(node.config) ? node.config : {};
  const configuredContent = typeof config.content === "string" ? config.content.trim() : "";
  if (configuredContent) return null;
  const statePath = node.state?.pending_input?.path.trim() ?? "";
  if (!statePath) return null;
  return {
    runId: interrupt.run_id,
    checkpointId: interrupt.checkpoint_id,
    nodeId: interrupt.node_id,
    statePath,
    message: interrupt.message || "The run is waiting for human input.",
  };
}

export function pendingHumanInputState(path: string, message: string): unknown {
  const segments = path.split(".").map((segment) => segment.trim()).filter(Boolean);
  const root: Record<string, unknown> = {};
  let cursor = root;
  for (const segment of segments.slice(0, -1)) {
    const next: Record<string, unknown> = {};
    cursor[segment] = next;
    cursor = next;
  }
  if (segments.length > 0) cursor[segments[segments.length - 1]] = message;
  return root;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
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

function runControlModeFromRun(run: RunRecord | null, interrupt?: RunInterrupt | null): "run" | "active" | "resume" {
  if (!run) return "run";
  if (isResumableRunStatus(run.status)) return hasResumeCheckpoint(run, interrupt) ? "resume" : "run";
  return isActiveRunStatus(run.status) ? "active" : "run";
}

function canResumeRun(run: RunRecord | null, interrupt?: RunInterrupt | null): boolean {
  return Boolean(run && isResumableRunStatus(run.status) && hasResumeCheckpoint(run, interrupt));
}

function hasResumeCheckpoint(run: RunRecord, interrupt?: RunInterrupt | null): boolean {
  return Boolean(run.last_checkpoint_id || (interrupt?.run_id === run.run_id && interrupt.checkpoint_id));
}

function isResumableRunStatus(status: string): boolean {
  return status === "paused" || status === "interrupted";
}

function isActiveRunStatus(status: string): boolean {
  return status === "pending" || status === "running";
}

function readStoredRunStatusVisible(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(RUN_STATUS_VISIBLE_STORAGE_KEY) === "true";
  } catch {
    return false;
  }
}

function writeStoredRunStatusVisible(visible: boolean): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(RUN_STATUS_VISIBLE_STORAGE_KEY, visible ? "true" : "false");
  } catch {
    // Ignore storage errors; the panel state still works for the current session.
  }
}

function matchesGraphIdentity(run: RunRecord, identity: GraphIdentity): boolean {
  return run.graph_id === identity.id && run.graph_version === identity.version;
}

function upsertRunFromEvent(
  runs: RunRecord[],
  event: RuntimeEvent,
  nextStatus: string,
  graphIdentity: GraphIdentity
): RunRecord[] {
  if (!event.run_id) return runs;
  let found = false;
  const updated = runs.map((run) => {
    if (run.run_id !== event.run_id) return run;
    found = true;
    const status = nextStatus || run.status;
    return {
      ...run,
      status,
      updated_at: event.timestamp,
      finished_at: isTerminalRunStatus(status) ? event.timestamp : run.finished_at,
      last_checkpoint_id: stringPayloadField(event.payload, "checkpoint_id") || run.last_checkpoint_id,
    };
  });
  if (found) return updated;

  const status = nextStatus || "running";
  return [
    ...updated,
    {
      run_id: event.run_id,
      graph_id: graphIdentity.id || "graph",
      graph_version: graphIdentity.version || "1.0",
      status,
      entry_node_id: stringPayloadField(event.payload, "entry_node_id"),
      last_checkpoint_id: stringPayloadField(event.payload, "checkpoint_id") || undefined,
      started_at: event.timestamp,
      updated_at: event.timestamp,
      finished_at: isTerminalRunStatus(status) ? event.timestamp : undefined,
    },
  ];
}

function stringPayloadField(payload: unknown, field: string): string {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return "";
  const value = (payload as Record<string, unknown>)[field];
  return typeof value === "string" ? value : "";
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

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  analyzeInitialStateRequirements,
  ApiError,
  commitGraph,
  deleteGraph,
  getGraphDetail,
  getRegistry,
  getTools,
  listGraphs,
} from "../api";
import {
  cacheServerGraphs,
  hydrateServerGraph,
  hydrateServerGraphResult,
  preferredServerGraph,
  rememberGraphID,
} from "../lib/localGraphs";
import { parseJSON, stringifyJSON } from "../lib/utils";
import { readStoredAutoDetectLoops, writeStoredAutoDetectLoops } from "../lib/loopPreferences";
import {
  defaultInitialState,
  sampleGraph,
} from "./workbench/constants";
import { GraphWorkspace } from "./workbench/GraphWorkspace";
import { RegistryDialog } from "./workbench/RegistryDialog";
import { RunStatusPanel } from "./workbench/RunStatusPanel";
import { SettingsDialog } from "./workbench/SettingsDialog";
import { WorkbenchShell } from "./workbench/WorkbenchShell";
import { AssistantPanel } from "./workbench/AssistantPanel";
import {
  resolveWorkspaceMode,
  type WorkspaceMode,
} from "./workbench/workspaceModeModel";
import { UserInputPromptDialog } from "./workbench/UserInputPromptDialog";
import { useWorkbenchRuns } from "./workbench/useWorkbenchRuns";
import type { ToastRecord, ToastTone } from "./workbench/graph-workspace/ToastStack";
import { validateGraph } from "./workbench/graph-workspace/utils";
import {
  applyRuntimeSettingsUpdate,
  runtimeSettingsUpload,
} from "./workbench/graph-workspace/graphSettingsEditorModel";
import { missingInitialStateRequirements } from "./workbench/workbenchRunModel";
import {
  effectiveInitialStateRequirements,
  graphAnalysisSignature,
  graphSaveIdentity,
  graphSaveSignature,
  isGraphSavePending,
  missingTriggerStateRequirements,
} from "./workbench/graphSyncModel";
import { isSaveShortcut, isToggleRunPanelShortcut } from "./workbench/utils";
import { useGraphTriggers } from "./workbench/graph-workspace/useGraphTriggers";
import type {
  GraphDefinition,
  GraphDetail,
  GraphInfo,
  GraphInitialStateAnalysis,
  GraphLoadResult,
  RegistryInfo,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  ToolDefinition,
  Trigger,
} from "../types";

export { pendingUserInputState, userInputPromptFromInterrupt } from "./workbench/userInputModel";

function triggerPayloadForCommit(trigger: Trigger): Record<string, unknown> {
  return Object.fromEntries(Object.entries(trigger).filter(([key]) => (
    key !== "target" && key !== "created_at" && key !== "updated_at"
  )));
}

interface CachedInitialStateAnalysis {
  signature: string;
  analysis: GraphInitialStateAnalysis;
}

interface PendingInitialStateAnalysis {
  signature: string;
  controller: AbortController;
  promise: Promise<GraphInitialStateAnalysis>;
}

const emptyRuntimeSettings: RuntimeSettings = {
  environment: {},
  environment_secrets: {},
  models: [],
  tool_permissions: [],
  tool_approvals: {},
};

export function WorkbenchPage() {
  const [definitionText, setDefinitionText] = useState(stringifyJSON(sampleGraph));
  const [initialStateText, setInitialStateText] = useState(stringifyJSON(defaultInitialState));
  const [graphInfo, setGraphInfo] = useState<GraphInfo | null>(null);
  const [initialStateAnalysis, setInitialStateAnalysis] = useState<GraphInitialStateAnalysis | null>(null);
  const [registry, setRegistry] = useState<RegistryInfo | null>(null);
  const [toolDefinitions, setToolDefinitions] = useState<ToolDefinition[]>([]);
  const [runtimeSettings, setRuntimeSettings] = useState<RuntimeSettings>(emptyRuntimeSettings);
  const [registryDialogOpen, setRegistryDialogOpen] = useState(false);
  const [settingsDialogOpen, setSettingsDialogOpen] = useState(false);
  const [graphId, setGraphId] = useState("debug_graph");
  const [graphVersion, setGraphVersion] = useState("1.0");
  const [initialRequirementsError, setInitialRequirementsError] = useState("");
  const [toasts, setToasts] = useState<ToastRecord[]>([]);
  const [busy, setBusy] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savedGraphSignatures, setSavedGraphSignatures] = useState<Record<string, string>>({});
  const [serverGraphRevision, setServerGraphRevision] = useState(0);
  const [serverStateLoaded, setServerStateLoaded] = useState(false);
  const [serverGraphsLoaded, setServerGraphsLoaded] = useState(false);
  const [workspaceMode, setWorkspaceMode] = useState<WorkspaceMode>("edit");
  const [autoDetectLoops, setAutoDetectLoops] = useState(readStoredAutoDetectLoops);
  const initialRequirementsCacheRef = useRef<CachedInitialStateAnalysis | null>(null);
  const initialRequirementsRequestRef = useRef<PendingInitialStateAnalysis | null>(null);
  const toastSeqRef = useRef(0);
  const savingRef = useRef(false);
  const graphTriggers = useGraphTriggers(graphId);
  const changeGraphID = useCallback((value: string, remember = true) => {
    setGraphId(value);
    if (remember) rememberGraphID(value);
  }, []);

  const changeAutoDetectLoops = useCallback((enabled: boolean) => {
    setAutoDetectLoops(enabled);
    writeStoredAutoDetectLoops(enabled);
  }, []);

  const definition = useMemo(() => {
    try {
      return parseJSON<GraphDefinition>(definitionText);
    } catch {
      return null;
    }
  }, [definitionText]);
  const directInitialRequirements = initialStateAnalysis?.direct ?? null;
  const initialRequirements = useMemo(
    () => initialStateAnalysis ? effectiveInitialStateRequirements(initialStateAnalysis) : null,
    [initialStateAnalysis]
  );
  const initialRequirementsSignature = useMemo(
    () => definition ? graphAnalysisSignature(definition, graphTriggers.triggers) : "",
    [definition, graphTriggers.triggers]
  );

  const currentGraphIdentity = useMemo(
    () => ({
      id: graphId || graphInfo?.id || "graph",
      version: graphVersion || graphInfo?.version || "1.0",
      sessionID: graphInfo?.id === graphId ? graphInfo.graph_session_id : undefined,
    }),
    [graphId, graphInfo?.graph_session_id, graphInfo?.id, graphInfo?.version, graphVersion]
  );
  const currentGraphSaveSignature = useMemo(
    () => definition
      ? graphSaveSignature(
          definition,
          runtimeSettingsUpload(runtimeSettings),
          currentGraphIdentity.id,
          currentGraphIdentity.version
        )
      : "",
    [currentGraphIdentity.id, currentGraphIdentity.version, definition, runtimeSettings]
  );
  const graphUnsaved = Boolean(
    graphTriggers.isUnsaved || (
      serverStateLoaded &&
      isGraphSavePending(
        currentGraphSaveSignature,
        savedGraphSignatures[graphSaveIdentity(currentGraphIdentity.id, currentGraphIdentity.version)]
      )
    )
  );
  const recordSavedGraph = useCallback((
    targetDefinition: GraphDefinition,
    targetSettings: RuntimeSettingsUpdate,
    targetGraphID: string,
    targetGraphVersion: string
  ) => {
    const identity = graphSaveIdentity(targetGraphID, targetGraphVersion);
    const signature = graphSaveSignature(
      targetDefinition,
      targetSettings,
      targetGraphID,
      targetGraphVersion
    );
    setSavedGraphSignatures((current) => ({ ...current, [identity]: signature }));
  }, []);

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

  const analyzeGraphDefinition = useCallback(
    (targetDefinition: GraphDefinition): Promise<GraphInitialStateAnalysis> => {
      const triggers = graphTriggers.analysisPayloads();
      const signature = graphAnalysisSignature(targetDefinition, graphTriggers.triggers);
      const cached = initialRequirementsCacheRef.current;
      if (cached?.signature === signature) return Promise.resolve(cached.analysis);

      const pending = initialRequirementsRequestRef.current;
      if (pending?.signature === signature) return pending.promise;
      pending?.controller.abort();

      const controller = new AbortController();
      const promise = analyzeInitialStateRequirements(graphId, targetDefinition, triggers, controller.signal)
        .then((analysis) => {
          if (!controller.signal.aborted) {
            initialRequirementsCacheRef.current = { signature, analysis };
          }
          return analysis;
        })
        .finally(() => {
          if (initialRequirementsRequestRef.current?.controller === controller) {
            initialRequirementsRequestRef.current = null;
          }
        });
      initialRequirementsRequestRef.current = { signature, controller, promise };
      return promise;
    },
    [graphId, graphTriggers.analysisPayloads, graphTriggers.triggers]
  );
  const analyzeGraphForInputForm = useCallback(
    async (targetDefinition: GraphDefinition): Promise<GraphInitialStateAnalysis> => {
      try {
        const analysis = await analyzeGraphDefinition(targetDefinition);
        setInitialStateAnalysis(analysis);
        setInitialRequirementsError("");
        return analysis;
      } catch (err) {
        setInitialRequirementsError(err instanceof Error ? err.message : String(err));
        throw err;
      }
    },
    [analyzeGraphDefinition]
  );

  const {
    runs,
    runTriggerTypes,
    selectedRunID,
    runInspectionLoading,
    steps,
    checkpoints,
    runComparison,
    runComparisonLoading,
    displayEvents,
    runtimeEvents,
    hasOlderEvents,
    olderEventsLoading,
    humanPrompt,
    humanPromptText,
    runStatusVisible,
    runBusy,
    runLaunchPending,
    graphSwitchLocked,
    canResumeSelectedRun,
    runControlMode,
    streamStatus,
    streamDiagnostics,
    reconnectEventStream,
    setHumanPromptText,
    selectRun,
    loadOlderEvents,
    hideRunStatus,
    toggleRunStatus,
    resetRunState,
    refreshRuns,
    startConfiguredRun,
    pauseSelectedRun,
    cancelSelectedRun,
    deleteRunRecord,
    forkSelectedRun,
    compareSelectedRun,
    resumeSelectedRun,
    submitUserInputPrompt,
    dismissUserInputPrompt,
  } = useWorkbenchRuns({
    graphIdentity: currentGraphIdentity,
    definition,
    initialStateText,
    onNotify: pushToast,
  });
  const selectedRun = useMemo(
    () => runs.find((run) => run.run_id === selectedRunID),
    [runs, selectedRunID]
  );
  const selectedRunCurrentNodeIds = useMemo(
    () => [...new Set([
      ...(selectedRun?.current_node_ids ?? []),
      ...(selectedRun?.current_node_id ? [selectedRun.current_node_id] : []),
    ])],
    [selectedRun?.current_node_id, selectedRun?.current_node_ids]
  );
  const initializing = !serverStateLoaded;
  const workbenchBusy = busy || runBusy || runLaunchPending;
  const runControlsDisabled = runBusy || initializing;
  const graphSwitchDisabled = workbenchBusy || graphSwitchLocked || initializing;
  const hasRunStatus = runs.length > 0 || Boolean(selectedRunID);
  const activeWorkspaceMode = resolveWorkspaceMode(workspaceMode, runStatusVisible);
  const showAutoRunPanel = useCallback(() => {
    if (workspaceMode !== "auto") return;
    if (!runStatusVisible) toggleRunStatus();
  }, [runStatusVisible, toggleRunStatus, workspaceMode]);

  const changeWorkspaceMode = useCallback((mode: WorkspaceMode) => {
    setWorkspaceMode(mode);
    if (mode === "debug" && !runStatusVisible) toggleRunStatus();
  }, [runStatusVisible, toggleRunStatus]);

  const loadServerState = useCallback(async () => {
    try {
      const [reg, tools, graphs] = await Promise.all([
        getRegistry().catch(() => null),
        getTools().catch(() => null),
        listGraphs().catch((err) => {
          notifyError(err);
          return [];
        }),
      ]);
      setRegistry(reg);
      setToolDefinitions(tools?.tools ?? []);
      const cachedGraphs = cacheServerGraphs(graphs);
      const selectedSummary = preferredServerGraph(graphs);
      let selectedDetail: GraphDetail | null = null;
      if (selectedSummary) {
        selectedDetail = await getGraphDetail(selectedSummary.id);
        const cachedGraph = cachedGraphs.find((graph) => graph.graphId === selectedSummary.id);
        if (cachedGraph) hydrateServerGraph(cachedGraph, selectedDetail);
        setGraphInfo(selectedDetail.graph);
        changeGraphID(selectedDetail.graph.id);
        setGraphVersion(selectedDetail.graph.version);
        setDefinitionText(stringifyJSON(selectedDetail.definition));
        setRuntimeSettings(selectedDetail.settings);
        const analysis: GraphInitialStateAnalysis = {
          direct: selectedDetail.initial_state_requirements,
          triggers: [],
        };
        setInitialStateAnalysis(analysis);
        setInitialRequirementsError("");
        initialRequirementsCacheRef.current = {
          signature: graphAnalysisSignature(selectedDetail.definition),
          analysis,
        };
      }
      const nextSavedGraphSignatures: Record<string, string> = {};
      if (selectedDetail) {
        nextSavedGraphSignatures[graphSaveIdentity(selectedDetail.graph.id, selectedDetail.graph.version)] = graphSaveSignature(
          selectedDetail.definition,
          runtimeSettingsUpload(selectedDetail.settings),
          selectedDetail.graph.id,
          selectedDetail.graph.version
        );
      }
      setSavedGraphSignatures(nextSavedGraphSignatures);
      setServerGraphsLoaded(true);
      const loadIdentity = selectedDetail
        ? {
            id: selectedDetail.graph.id,
            version: selectedDetail.graph.version,
            sessionID: selectedDetail.graph.graph_session_id,
          }
        : undefined;
      await refreshRuns(loadIdentity, true).catch(() => undefined);
    } catch (err) {
      notifyError(err);
    } finally {
      setServerStateLoaded(true);
    }
  }, [changeGraphID, notifyError, refreshRuns]);

  useEffect(() => {
    void loadServerState();
  }, [loadServerState]);

  const prepareGraphSwitch = useCallback(() => {
    if (graphSwitchDisabled) {
      pushToast(
        "warn",
        workbenchBusy
          ? "Cannot switch graph while an operation is in progress"
          : "Cannot switch graph while a run is active"
      );
      return false;
    }
    resetRunState();
    return true;
  }, [graphSwitchDisabled, pushToast, resetRunState, workbenchBusy]);

  useEffect(() => {
    if (!serverStateLoaded) return;
    if (!definition || validateGraph(definition, registry)) {
      setInitialStateAnalysis(null);
      setInitialRequirementsError("");
      return;
    }
    const cached = initialRequirementsCacheRef.current;
    if (cached?.signature === initialRequirementsSignature) {
      setInitialStateAnalysis(cached.analysis);
      setInitialRequirementsError("");
      return;
    }
    setInitialStateAnalysis(null);
    setInitialRequirementsError("");
  }, [definition, initialRequirementsSignature, registry, serverStateLoaded]);

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
        return;
      }
      const analysis = await analyzeGraphForInputForm(definition);

      const initialState = parseJSON<unknown>(initialStateText);
      const missingInitialState = missingInitialStateRequirements(initialState, [
        ...analysis.direct.required,
        ...analysis.direct.unresolved,
      ]);
      if (missingInitialState.length > 0) {
        const preview = missingInitialState.slice(0, 4).join(", ");
        const suffix = missingInitialState.length > 4 ? ` (+${missingInitialState.length - 4} more)` : "";
        pushToast("error", `Missing initial state: ${preview}${suffix}`);
        return;
      }
      const result = await commitCurrentGraph(definition);
      if (!result) return;
      showAutoRunPanel();
      await startConfiguredRun(initialState, {
        id: result.graph.id,
        version: result.graph.version,
        sessionID: result.graph.graph_session_id,
      });
    } catch (err) {
      notifyError(err);
    } finally {
      setBusy(false);
    }
  }

  const changeRuntimeSettings = useCallback((settings: RuntimeSettingsUpdate): RuntimeSettings => {
    const next = applyRuntimeSettingsUpdate(runtimeSettings, settings);
    setRuntimeSettings(next);
    return next;
  }, [runtimeSettings]);

  const replaceRuntimeSettings = useCallback((settings: RuntimeSettings) => {
    setRuntimeSettings(settings);
  }, []);

  const handleGraphDetailLoaded = useCallback((detail: GraphDetail) => {
    setGraphInfo(detail.graph);
    const analysis: GraphInitialStateAnalysis = {
      direct: detail.initial_state_requirements,
      triggers: [],
    };
    setInitialStateAnalysis(analysis);
    setInitialRequirementsError("");
    initialRequirementsCacheRef.current = {
      signature: graphAnalysisSignature(detail.definition),
      analysis,
    };
    recordSavedGraph(
      detail.definition,
      runtimeSettingsUpload(detail.settings),
      detail.graph.id,
      detail.graph.version
    );
  }, [recordSavedGraph]);

  const refreshAssistantGraph = useCallback(async () => {
    const detail = await getGraphDetail(graphId);
    setGraphId(detail.graph.id);
    setGraphVersion(detail.graph.version);
    setDefinitionText(stringifyJSON(detail.definition));
    setRuntimeSettings(detail.settings);
    handleGraphDetailLoaded(detail);
  }, [graphId, handleGraphDetailLoaded]);

  async function saveGraph() {
    if (savingRef.current) return;
    if (!graphUnsaved) return;
    savingRef.current = true;
    setBusy(true);
    setSaving(true);
    try {
      if (!definition) {
        pushToast("error", "Graph JSON is invalid");
        return;
      }
      const graphValidationError = validateGraph(definition, registry);
      if (graphValidationError) {
        pushToast("error", `Graph validation failed: ${graphValidationError}`);
        return;
      }
      if (graphTriggers.isUnsaved) graphTriggers.validate();

      const analysis = await analyzeGraphForInputForm(definition);
      const missingTriggerState = missingTriggerStateRequirements(analysis);
      if (missingTriggerState.length > 0) {
        const first = missingTriggerState[0];
        const preview = first.paths.slice(0, 4).join(", ");
        const suffix = first.paths.length > 4 ? ` (+${first.paths.length - 4} more)` : "";
        pushToast("error", `Trigger "${first.triggerID}" is missing graph state: ${preview}${suffix}`);
        return;
      }

      const result = await commitCurrentGraph(definition);
      if (!result) return;
      const session = result.graph.graph_session_id ? ` (${result.graph.graph_session_id})` : "";
      pushToast("info", `Saved ${result.graph.id}@${result.graph.version}${session}`);
    } catch (err) {
      notifyError(err);
    } finally {
      savingRef.current = false;
      setSaving(false);
      setBusy(false);
    }
  }

  async function commitCurrentGraph(targetDefinition: GraphDefinition) {
    let expectedGraphSessionID = graphInfo?.id === graphId ? graphInfo.graph_session_id : undefined;
    let mode: "create" | "overwrite" = expectedGraphSessionID ? "overwrite" : "create";
    if (!expectedGraphSessionID) {
      try {
        const detail = await getGraphDetail(graphId);
        expectedGraphSessionID = detail.graph.graph_session_id;
        mode = "overwrite";
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 404) throw error;
      }
    }
    const result = await commitGraph(
      graphId,
      targetDefinition,
      runtimeSettingsUpload(runtimeSettings),
      graphTriggers.analysisPayloads(),
      mode,
      expectedGraphSessionID,
      graphVersion
    );
    await applyGraphCommitResult(result);
    return result;
  }

  async function commitImportedGraph(input: {
    definition: GraphDefinition;
    graphID: string;
    graphVersion: string;
    settings: RuntimeSettings;
    triggers: Trigger[];
    mode: "create" | "overwrite";
    expectedGraphSessionID?: string;
  }) {
    const result = await commitGraph(
      input.graphID,
      input.definition,
      runtimeSettingsUpload(input.settings),
      input.triggers.map(triggerPayloadForCommit),
      input.mode,
      input.expectedGraphSessionID,
      input.graphVersion
    );
    await applyGraphCommitResult(result);
    return result;
  }

  async function applyGraphCommitResult(result: GraphLoadResult) {
    graphTriggers.acceptCommit(result.triggers);
    const nextRuntimeSettings = result.settings;
    setGraphInfo(result.graph);
    changeGraphID(result.graph.id);
    setGraphVersion(result.graph.version);
    setRuntimeSettings(nextRuntimeSettings);
    recordSavedGraph(
      result.definition,
      runtimeSettingsUpload(nextRuntimeSettings),
      result.graph.id,
      result.graph.version
    );
    const summaries = await listGraphs();
    const cachedGraphs = cacheServerGraphs(summaries);
    const committedGraph = cachedGraphs.find((graph) => graph.graphId === result.graph.id);
    if (committedGraph) hydrateServerGraphResult(committedGraph, result);
    setServerGraphRevision((revision) => revision + 1);
  }

  async function deleteServerGraph(graphID: string) {
    setBusy(true);
    try {
      await deleteGraph(graphID);
      const summaries = await listGraphs();
      cacheServerGraphs(summaries);
      if (graphInfo?.id === graphID) {
        setGraphInfo(null);
        setInitialStateAnalysis(null);
        setInitialRequirementsError("");
        initialRequirementsCacheRef.current = null;
      }
      setServerGraphRevision((revision) => revision + 1);
      pushToast("info", `Deleted ${graphID}`);
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    const handleSaveShortcut = (event: KeyboardEvent) => {
      if (initializing || settingsDialogOpen || !isSaveShortcut(event) || !graphUnsaved) return;
      event.preventDefault();
      if (event.repeat || workbenchBusy || !definition) return;
      void saveGraph();
    };
    window.addEventListener("keydown", handleSaveShortcut);
    return () => window.removeEventListener("keydown", handleSaveShortcut);
  });

  useEffect(() => {
    const handleToggleRunPanelShortcut = (event: KeyboardEvent) => {
      if (
        initializing ||
        registryDialogOpen ||
        settingsDialogOpen ||
        humanPrompt ||
        (!hasRunStatus && !runStatusVisible) ||
        !isToggleRunPanelShortcut(event)
      ) return;
      event.preventDefault();
      if (event.repeat) return;
      toggleRunStatus();
    };
    window.addEventListener("keydown", handleToggleRunPanelShortcut);
    return () => window.removeEventListener("keydown", handleToggleRunPanelShortcut);
  }, [hasRunStatus, humanPrompt, initializing, registryDialogOpen, runStatusVisible, settingsDialogOpen, toggleRunStatus]);

  return (
    <WorkbenchShell
      streamStatus={streamStatus}
      streamDiagnostics={streamDiagnostics}
      onReconnectEventStream={reconnectEventStream}
      busy={workbenchBusy}
      runBusy={runBusy}
      runLaunchPending={runLaunchPending}
      initializing={initializing}
      saving={saving}
      unsaved={graphUnsaved}
      definition={definition}
      runControlMode={runControlMode}
      workspaceMode={workspaceMode}
      canResume={canResumeSelectedRun}
      runControlsDisabled={runControlsDisabled}
      onRun={runGraph}
      onSave={() => void saveGraph()}
      onPause={() => void pauseSelectedRun()}
      onStop={() => void cancelSelectedRun()}
      onResume={() => {
        showAutoRunPanel();
        void resumeSelectedRun();
      }}
      onShowRegistry={() => setRegistryDialogOpen(true)}
      onShowSettings={() => setSettingsDialogOpen(true)}
      hasRunStatus={hasRunStatus}
      runStatusVisible={runStatusVisible}
      onToggleRunStatus={toggleRunStatus}
      onWorkspaceModeChange={changeWorkspaceMode}
      runStatusPanel={
        <RunStatusPanel
          runs={runs}
          runTriggerTypes={runTriggerTypes}
          selectedRunId={selectedRunID}
          loading={initializing}
          runInspectionLoading={runInspectionLoading}
          runActionsDisabled={workbenchBusy}
          onSelectRun={(runID) => {
            showAutoRunPanel();
            selectRun(runID);
          }}
          onDeleteRun={(runID) => void deleteRunRecord(runID)}
          onForkRun={() => void forkSelectedRun()}
          onCompareRuns={(runID) => void compareSelectedRun(runID)}
          runComparison={runComparison}
          runComparisonLoading={runComparisonLoading}
          steps={steps}
          checkpoints={checkpoints}
          events={displayEvents}
          hasOlderEvents={hasOlderEvents}
          olderEventsLoading={olderEventsLoading}
          onLoadOlderEvents={() => void loadOlderEvents()}
          onHide={hideRunStatus}
        />
      }
    >
      <GraphWorkspace
        workspaceMode={activeWorkspaceMode}
        initializing={initializing}
        definition={definition}
        definitionText={definitionText}
        initialStateText={initialStateText}
        initialRequirements={initialRequirements}
        directInitialRequirements={directInitialRequirements}
        initialRequirementsError={initialRequirementsError}
        steps={steps}
        runtimeEvents={runtimeEvents}
        selectedRunId={selectedRunID}
        runStatus={selectedRun?.status}
        runTriggerId={selectedRun?.origin?.trigger_id}
        currentNodeIds={selectedRunCurrentNodeIds}
        runUpdatedAt={selectedRun?.updated_at}
        registry={registry}
        toolDefinitions={toolDefinitions}
        runtimeSettings={runtimeSettings}
        autoDetectLoops={autoDetectLoops}
        graphTriggers={graphTriggers}
        onChangeRuntimeSettings={changeRuntimeSettings}
        onReplaceRuntimeSettings={replaceRuntimeSettings}
        graphId={graphId}
        graphVersion={graphVersion}
        serverGraphRevision={serverGraphRevision}
        serverGraphsLoaded={serverGraphsLoaded}
        graphSwitchDisabled={graphSwitchDisabled}
        toasts={toasts}
        onGraphId={changeGraphID}
        onGraphVersion={setGraphVersion}
        onDefinitionText={setDefinitionText}
        onInitialStateText={setInitialStateText}
        onDismissToast={dismissToast}
        onNotify={pushToast}
        onCommitGraphImport={commitImportedGraph}
        onDeleteServerGraph={deleteServerGraph}
        onGraphSwitch={prepareGraphSwitch}
        onGraphDetailLoaded={handleGraphDetailLoaded}
      />
      <UserInputPromptDialog
        prompt={humanPrompt}
        value={humanPromptText}
        busy={workbenchBusy}
        onChange={setHumanPromptText}
        onCancel={dismissUserInputPrompt}
        onSubmit={() => void submitUserInputPrompt()}
      />
      <RegistryDialog
        open={registryDialogOpen}
        registry={registry}
        toolDefinitions={toolDefinitions}
        onClose={() => setRegistryDialogOpen(false)}
      />
      <SettingsDialog
        open={settingsDialogOpen}
        onClose={() => setSettingsDialogOpen(false)}
        autoDetectLoops={autoDetectLoops}
        onAutoDetectLoopsChange={changeAutoDetectLoops}
      />
      <AssistantPanel
        graphID={graphId}
        graphVersion={graphVersion}
        definition={definition}
        selectedRunID={selectedRunID}
        workspaceMode={activeWorkspaceMode}
        onGraphRefresh={refreshAssistantGraph}
      />
    </WorkbenchShell>
  );
}

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  analyzeInitialStateRequirements,
  createGraphSession,
  getGraphDetail,
  getRegistry,
  getTools,
  listGraphs,
} from "../api";
import { cacheServerGraphs, hydrateServerGraph } from "../lib/localGraphs";
import { parseJSON, stringifyJSON } from "../lib/utils";
import {
  defaultInitialState,
  sampleGraph,
  workspaceTabs,
  type WorkspaceTab,
} from "./workbench/constants";
import { GraphWorkspace } from "./workbench/GraphWorkspace";
import { RegistryDialog } from "./workbench/RegistryDialog";
import { RunStatusPanel } from "./workbench/RunStatusPanel";
import { SettingsWorkspace } from "./workbench/SettingsWorkspace";
import { WorkbenchShell } from "./workbench/WorkbenchShell";
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
import { isSaveShortcut } from "./workbench/utils";
import { useGraphTriggers } from "./workbench/graph-workspace/useGraphTriggers";
import type {
  GraphDefinition,
  GraphDetail,
  GraphInfo,
  GraphInitialStateAnalysis,
  RegistryInfo,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  ToolDefinition,
} from "../types";

export { workspaceTabs };
export type { WorkspaceTab };
export { pendingUserInputState, userInputPromptFromInterrupt } from "./workbench/userInputModel";

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
  models: [],
  memory: { enabled: false },
};

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
  const [initialStateAnalysis, setInitialStateAnalysis] = useState<GraphInitialStateAnalysis | null>(null);
  const [registry, setRegistry] = useState<RegistryInfo | null>(null);
  const [toolDefinitions, setToolDefinitions] = useState<ToolDefinition[]>([]);
  const [runtimeSettings, setRuntimeSettings] = useState<RuntimeSettings>(emptyRuntimeSettings);
  const [registryDialogOpen, setRegistryDialogOpen] = useState(false);
  const [graphId, setGraphId] = useState("debug_graph");
  const [graphVersion, setGraphVersion] = useState("1.0");
  const [initialRequirementsError, setInitialRequirementsError] = useState("");
  const [toasts, setToasts] = useState<ToastRecord[]>([]);
  const [busy, setBusy] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savedGraphSignatures, setSavedGraphSignatures] = useState<Record<string, string>>({});
  const [serverStateLoaded, setServerStateLoaded] = useState(false);
  const [serverGraphsLoaded, setServerGraphsLoaded] = useState(false);
  const initialRequirementsCacheRef = useRef<CachedInitialStateAnalysis | null>(null);
  const initialRequirementsRequestRef = useRef<PendingInitialStateAnalysis | null>(null);
  const toastSeqRef = useRef(0);
  const savingRef = useRef(false);
  const graphTriggers = useGraphTriggers(graphId);

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

  const {
    runs,
    runTriggerTypes,
    selectedRunID,
    runInspectionLoading,
    steps,
    checkpoints,
    displayEvents,
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
    resumeSelectedRun,
    submitUserInputPrompt,
    dismissUserInputPrompt,
  } = useWorkbenchRuns({
    graphIdentity: currentGraphIdentity,
    definition,
    initialStateText,
    onNotify: pushToast,
  });
  const workbenchBusy = busy || runBusy || runLaunchPending;
  const runControlsDisabled = runBusy || (busy && !runLaunchPending);
  const graphSwitchDisabled = workbenchBusy || graphSwitchLocked;

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
      const firstSummary = graphs[0];
      let firstDetail: GraphDetail | null = null;
      if (firstSummary) {
        firstDetail = await getGraphDetail(firstSummary.id);
        const cachedGraph = cachedGraphs.find((graph) => graph.graphId === firstSummary.id);
        if (cachedGraph) hydrateServerGraph(cachedGraph, firstDetail);
        setGraphInfo(firstDetail.graph);
        setGraphId(firstDetail.graph.id);
        setGraphVersion(firstDetail.graph.version);
        setDefinitionText(stringifyJSON(firstDetail.definition));
        setRuntimeSettings(firstDetail.settings);
        const analysis: GraphInitialStateAnalysis = {
          direct: firstDetail.initial_state_requirements,
          triggers: [],
        };
        setInitialStateAnalysis(analysis);
        setInitialRequirementsError("");
        initialRequirementsCacheRef.current = {
          signature: graphAnalysisSignature(firstDetail.definition),
          analysis,
        };
      }
      const nextSavedGraphSignatures: Record<string, string> = {};
      if (firstDetail) {
        nextSavedGraphSignatures[graphSaveIdentity(firstDetail.graph.id, firstDetail.graph.version)] = graphSaveSignature(
          firstDetail.definition,
          runtimeSettingsUpload(firstDetail.settings),
          firstDetail.graph.id,
          firstDetail.graph.version
        );
      }
      setSavedGraphSignatures(nextSavedGraphSignatures);
      setServerGraphsLoaded(true);
      const loadIdentity = firstDetail
        ? {
            id: firstDetail.graph.id,
            version: firstDetail.graph.version,
            sessionID: firstDetail.graph.graph_session_id,
          }
        : undefined;
      await refreshRuns(loadIdentity, true).catch(() => undefined);
    } catch (err) {
      notifyError(err);
    } finally {
      setServerStateLoaded(true);
    }
  }, [notifyError, refreshRuns]);

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
        setTab("graph");
        return;
      }
      const analysis = await analyzeGraphDefinition(definition);
      setInitialStateAnalysis(analysis);
      setInitialRequirementsError("");

      const initialState = parseJSON<unknown>(initialStateText);
      const missingInitialState = missingInitialStateRequirements(initialState, [
        ...analysis.direct.required,
        ...analysis.direct.unresolved,
      ]);
      if (missingInitialState.length > 0) {
        const preview = missingInitialState.slice(0, 4).join(", ");
        const suffix = missingInitialState.length > 4 ? ` (+${missingInitialState.length - 4} more)` : "";
        pushToast("error", `Missing initial state: ${preview}${suffix}`);
        setTab("graph");
        return;
      }
      const settings = runtimeSettingsUpload(runtimeSettings);
      const result = await createGraphSession(graphId, definition, settings, graphVersion);
      const nextRuntimeSettings = applyRuntimeSettingsUpdate(result.settings, settings);
      setGraphInfo(result.graph);
      setGraphId(result.graph.id);
      setGraphVersion(result.graph.version);
      setRuntimeSettings(nextRuntimeSettings);
      recordSavedGraph(
        definition,
        runtimeSettingsUpload(nextRuntimeSettings),
        result.graph.id,
        result.graph.version
      );
      await startConfiguredRun(initialState, {
        id: result.graph.id,
        version: result.graph.version,
        sessionID: result.graph.graph_session_id,
      });
    } catch (err) {
      setInitialRequirementsError(err instanceof Error ? err.message : String(err));
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

  async function saveGraph() {
    if (savingRef.current) return;
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

      const analysis = await analyzeGraphDefinition(definition);
      setInitialStateAnalysis(analysis);
      setInitialRequirementsError("");
      const missingTriggerState = missingTriggerStateRequirements(analysis);
      if (missingTriggerState.length > 0) {
        const first = missingTriggerState[0];
        const preview = first.paths.slice(0, 4).join(", ");
        const suffix = first.paths.length > 4 ? ` (+${first.paths.length - 4} more)` : "";
        pushToast("error", `Trigger "${first.triggerID}" is missing graph state: ${preview}${suffix}`);
        return;
      }

      const settings = runtimeSettingsUpload(runtimeSettings);
      const result = await createGraphSession(graphId, definition, settings, graphVersion);
      const nextRuntimeSettings = applyRuntimeSettingsUpdate(result.settings, settings);
      setGraphInfo(result.graph);
      setGraphId(result.graph.id);
      setGraphVersion(result.graph.version);
      setRuntimeSettings(nextRuntimeSettings);
      recordSavedGraph(
        definition,
        runtimeSettingsUpload(nextRuntimeSettings),
        result.graph.id,
        result.graph.version
      );
      if (graphTriggers.isUnsaved) await graphTriggers.save();
      const session = result.graph.graph_session_id ? ` (${result.graph.graph_session_id})` : "";
      pushToast("info", `Saved ${result.graph.id}@${result.graph.version}${session}`);
    } catch (err) {
      setInitialRequirementsError(err instanceof Error ? err.message : String(err));
      notifyError(err);
    } finally {
      savingRef.current = false;
      setSaving(false);
      setBusy(false);
    }
  }

  useEffect(() => {
    const handleSaveShortcut = (event: KeyboardEvent) => {
      if (tab !== "graph" || !isSaveShortcut(event)) return;
      event.preventDefault();
      if (event.repeat || workbenchBusy || !definition) return;
      void saveGraph();
    };
    window.addEventListener("keydown", handleSaveShortcut);
    return () => window.removeEventListener("keydown", handleSaveShortcut);
  });

  return (
    <WorkbenchShell
      tab={tab}
      streamStatus={streamStatus}
      busy={workbenchBusy}
      saving={saving}
      unsaved={graphUnsaved}
      definition={definition}
      runControlMode={runControlMode}
      canResume={canResumeSelectedRun}
      runControlsDisabled={runControlsDisabled}
      onRun={runGraph}
      onSave={() => void saveGraph()}
      onPause={() => void pauseSelectedRun()}
      onStop={() => void cancelSelectedRun()}
      onResume={() => void resumeSelectedRun()}
      onShowRegistry={() => setRegistryDialogOpen(true)}
      onTabChange={setTab}
      hasRunStatus={runs.length > 0 || Boolean(selectedRunID)}
      runStatusVisible={runStatusVisible}
      onToggleRunStatus={toggleRunStatus}
      runStatusPanel={
        <RunStatusPanel
          runs={runs}
          runTriggerTypes={runTriggerTypes}
          selectedRunId={selectedRunID}
          runInspectionLoading={runInspectionLoading}
          runActionsDisabled={workbenchBusy}
          onSelectRun={selectRun}
          onDeleteRun={(runID) => void deleteRunRecord(runID)}
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
      {tab === "graph" ? (
        <GraphWorkspace
          definition={definition}
          definitionText={definitionText}
          initialStateText={initialStateText}
          initialRequirements={initialRequirements}
          directInitialRequirements={directInitialRequirements}
          initialRequirementsError={initialRequirementsError}
          steps={steps}
          selectedRunId={selectedRunID}
          registry={registry}
          toolDefinitions={toolDefinitions}
          runtimeSettings={runtimeSettings}
          graphTriggers={graphTriggers}
          onChangeRuntimeSettings={changeRuntimeSettings}
          onReplaceRuntimeSettings={replaceRuntimeSettings}
          graphId={graphId}
          graphVersion={graphVersion}
          serverGraphsLoaded={serverGraphsLoaded}
          graphSwitchDisabled={graphSwitchDisabled}
          toasts={toasts}
          onGraphId={setGraphId}
          onGraphVersion={setGraphVersion}
          onDefinitionText={setDefinitionText}
          onInitialStateText={setInitialStateText}
          onDismissToast={dismissToast}
          onGraphSwitch={prepareGraphSwitch}
          onGraphDetailLoaded={handleGraphDetailLoaded}
        />
      ) : null}
      {tab === "settings" ? (
        <SettingsWorkspace registry={registry} />
      ) : null}
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
    </WorkbenchShell>
  );
}

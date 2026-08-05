import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  analyzeInitialStateRequirements,
  getGraphDefinition,
  getGraphInfo,
  getInitialStateRequirements,
  getRegistry,
  getRuntimeSettings,
  getTools,
  listGraphs,
  setGraphDefinition,
} from "../api";
import { cacheServerGraphs } from "../lib/localGraphs";
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
  graphAnalysisSignature,
  graphSaveIdentity,
  graphSaveSignature,
  isGraphSavePending,
} from "./workbench/graphSyncModel";
import { isSaveShortcut } from "./workbench/utils";
import { useGraphTriggers } from "./workbench/graph-workspace/useGraphTriggers";
import type {
  GraphDefinition,
  GraphInfo,
  InitialStateRequirements,
  RegistryInfo,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  ToolDefinition,
} from "../types";

export { workspaceTabs };
export type { WorkspaceTab };
export { pendingUserInputState, userInputPromptFromInterrupt } from "./workbench/userInputModel";

interface CachedInitialStateRequirements {
  signature: string;
  requirements: InitialStateRequirements;
}

interface PendingInitialStateRequirements {
  signature: string;
  controller: AbortController;
  promise: Promise<InitialStateRequirements>;
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
  const [initialRequirements, setInitialRequirements] = useState<InitialStateRequirements | null>(null);
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
  const initialRequirementsCacheRef = useRef<CachedInitialStateRequirements | null>(null);
  const initialRequirementsRequestRef = useRef<PendingInitialStateRequirements | null>(null);
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
  const initialRequirementsSignature = useMemo(
    () => definition ? graphAnalysisSignature(definition) : "",
    [definition]
  );

  const currentGraphIdentity = useMemo(
    () => ({
      id: graphId || graphInfo?.id || "graph",
      version: graphVersion || graphInfo?.version || "1.0",
    }),
    [graphId, graphInfo?.id, graphInfo?.version, graphVersion]
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
    (targetDefinition: GraphDefinition): Promise<InitialStateRequirements> => {
      const signature = graphAnalysisSignature(targetDefinition);
      const cached = initialRequirementsCacheRef.current;
      if (cached?.signature === signature) return Promise.resolve(cached.requirements);

      const pending = initialRequirementsRequestRef.current;
      if (pending?.signature === signature) return pending.promise;
      pending?.controller.abort();

      const controller = new AbortController();
      const promise = analyzeInitialStateRequirements(targetDefinition, controller.signal)
        .then((requirements) => {
          if (!controller.signal.aborted) {
            initialRequirementsCacheRef.current = { signature, requirements };
          }
          return requirements;
        })
        .finally(() => {
          if (initialRequirementsRequestRef.current?.controller === controller) {
            initialRequirementsRequestRef.current = null;
          }
        });
      initialRequirementsRequestRef.current = { signature, controller, promise };
      return promise;
    },
    []
  );

  const {
    runs,
    runTriggerTypes,
    selectedRunID,
    steps,
    checkpoints,
    displayEvents,
    hasOlderEvents,
    olderEventsLoading,
    humanPrompt,
    humanPromptText,
    runStatusVisible,
    runBusy,
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
  const workbenchBusy = busy || runBusy;

  const refreshInitialRequirements = useCallback(async (targetDefinition?: GraphDefinition) => {
    initialRequirementsRequestRef.current?.controller.abort();
    initialRequirementsRequestRef.current = null;
    const requirements = await getInitialStateRequirements();
    if (targetDefinition) {
      initialRequirementsCacheRef.current = {
        signature: graphAnalysisSignature(targetDefinition),
        requirements,
      };
    }
    setInitialRequirements(requirements);
    setInitialRequirementsError("");
    return requirements;
  }, []);

  const loadServerState = useCallback(async () => {
    try {
      const [info, reg, tools, settings, graphs] = await Promise.all([
        getGraphInfo().catch(() => null),
        getRegistry().catch(() => null),
        getTools().catch(() => null),
        getRuntimeSettings().catch(() => null),
        listGraphs().catch((err) => {
          notifyError(err);
          return [];
        }),
      ]);
      setGraphInfo(info);
      setRegistry(reg);
      setToolDefinitions(tools?.tools ?? []);
      cacheServerGraphs(graphs);
      const nextSavedGraphSignatures = Object.fromEntries(
        graphs.map((graph) => [
          graphSaveIdentity(graph.id, graph.graph_version),
          graphSaveSignature(
            graph.definition,
            runtimeSettingsUpload(graph.settings),
            graph.id,
            graph.graph_version
          ),
        ])
      );
      if (info) {
        const cachedGraph = graphs.find((graph) =>
          graph.id === info.id && graph.graph_version === info.version
        ) ?? graphs.find((graph) => graph.id === info.id);
        const serverDefinition = await getGraphDefinition().catch(() => null) ?? cachedGraph?.definition;
        const serverSettings = settings ?? cachedGraph?.settings ?? emptyRuntimeSettings;
        setGraphId(info.id);
        setGraphVersion(info.version);
        setRuntimeSettings(serverSettings);
        if (serverDefinition) setDefinitionText(stringifyJSON(serverDefinition));
        if (serverDefinition) {
          nextSavedGraphSignatures[graphSaveIdentity(info.id, info.version)] = graphSaveSignature(
            serverDefinition,
            runtimeSettingsUpload(serverSettings),
            info.id,
            info.version
          );
        }
        await refreshInitialRequirements(serverDefinition ?? undefined).catch((err) => {
          setInitialRequirements(null);
          setInitialRequirementsError(err instanceof Error ? err.message : String(err));
        });
      } else if (graphs[0]) {
        setGraphId(graphs[0].id);
        setGraphVersion(graphs[0].graph_version);
        setDefinitionText(stringifyJSON(graphs[0].definition));
        setRuntimeSettings(graphs[0].settings);
      } else if (settings) {
        setRuntimeSettings(settings);
      }
      setSavedGraphSignatures(nextSavedGraphSignatures);
      setServerGraphsLoaded(true);
      const initialGraph = info ?? graphs[0];
      const loadIdentity = initialGraph
        ? {
            id: initialGraph.id || "graph",
            version: "version" in initialGraph
              ? initialGraph.version || "1.0"
              : initialGraph.graph_version || "1.0",
          }
        : undefined;
      await refreshRuns(loadIdentity, true).catch(() => undefined);
    } catch (err) {
      notifyError(err);
    } finally {
      setServerStateLoaded(true);
    }
  }, [notifyError, refreshInitialRequirements, refreshRuns]);

  useEffect(() => {
    void loadServerState();
  }, [loadServerState]);

  const prepareGraphSwitch = useCallback(() => {
    if (graphSwitchLocked) {
      pushToast("warn", "Cannot switch graph while a run is active");
      return false;
    }
    resetRunState();
    return true;
  }, [graphSwitchLocked, pushToast, resetRunState]);

  useEffect(() => {
    if (!serverStateLoaded) return;
    if (!definition || validateGraph(definition, registry)) {
      setInitialRequirements(null);
      setInitialRequirementsError("");
      return;
    }
    const cached = initialRequirementsCacheRef.current;
    if (cached?.signature === initialRequirementsSignature) {
      setInitialRequirements(cached.requirements);
      setInitialRequirementsError("");
      return;
    }
    setInitialRequirements(null);
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
      const requirements = await analyzeGraphDefinition(definition);
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

      const settings = runtimeSettingsUpload(runtimeSettings);
      const result = await setGraphDefinition(definition, settings, graphId, graphVersion);
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
      await startConfiguredRun(initialState);
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

      const requirements = await analyzeGraphDefinition(definition);
      setInitialRequirements(requirements);
      setInitialRequirementsError("");
      if (requirements.unresolved.length > 0) {
        const unresolved = requirements.unresolved.map((item) => item.path).slice(0, 4).join(", ");
        const suffix = requirements.unresolved.length > 4 ? ` (+${requirements.unresolved.length - 4} more)` : "";
        pushToast("error", `Unresolved state requirements: ${unresolved}${suffix}`);
        return;
      }

      const settings = runtimeSettingsUpload(runtimeSettings);
      const result = await setGraphDefinition(definition, settings, graphId, graphVersion);
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
          graphSwitchDisabled={graphSwitchLocked}
          toasts={toasts}
          onGraphId={setGraphId}
          onGraphVersion={setGraphVersion}
          onDefinitionText={setDefinitionText}
          onInitialStateText={setInitialStateText}
          onDismissToast={dismissToast}
          onGraphSwitch={prepareGraphSwitch}
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

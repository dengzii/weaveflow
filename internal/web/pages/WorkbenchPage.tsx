import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  analyzeInitialStateRequirements,
  getGraphDefinition,
  getGraphInfo,
  getInitialStateRequirements,
  getRegistry,
  getRuntimeSettings,
  getTools,
  publishGraphDefinition,
  setGraphDefinition,
  updateRuntimeSettings,
} from "../api";
import { parseJSON, stringifyJSON } from "../lib/utils";
import { pickInitialLocalGraphDraft, readLocalGraphDrafts } from "../lib/localGraphs";
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
import { missingInitialStateRequirements } from "./workbench/workbenchRunModel";
import {
  graphPublishRequired,
  graphUploadRequired,
  graphUploadSignature,
  type SyncedGraphState,
} from "./workbench/graphSyncModel";
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
  const [runtimeSettings, setRuntimeSettings] = useState<RuntimeSettings | null>(null);
  const [registryDialogOpen, setRegistryDialogOpen] = useState(false);
  const [graphId, setGraphId] = useState("debug_graph");
  const [graphVersion, setGraphVersion] = useState("1.0");
  const [initialRequirementsError, setInitialRequirementsError] = useState("");
  const [toasts, setToasts] = useState<ToastRecord[]>([]);
  const [busy, setBusy] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const preferLocalGraphRef = useRef(false);
  const checkedLocalGraphRef = useRef(false);
  const syncedGraphRef = useRef<SyncedGraphState | null>(null);
  const toastSeqRef = useRef(0);

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

  const currentGraphIdentity = useMemo(
    () => ({
      id: graphId || graphInfo?.id || "graph",
      version: graphVersion || graphInfo?.version || "1.0",
    }),
    [graphId, graphInfo?.id, graphInfo?.version, graphVersion]
  );

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

  const {
    runs,
    runTriggerTypes,
    selectedRunID,
    steps,
    displayEvents,
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

  const refreshInitialRequirements = useCallback(async () => {
    const requirements = await getInitialStateRequirements();
    setInitialRequirements(requirements);
    setInitialRequirementsError("");
    return requirements;
  }, []);

  const loadServerState = useCallback(async () => {
    try {
      const [info, reg, tools, settings] = await Promise.all([
        getGraphInfo().catch(() => null),
        getRegistry().catch(() => null),
        getTools().catch(() => null),
        getRuntimeSettings().catch(() => null),
      ]);
      setGraphInfo(info);
      setRegistry(reg);
      setToolDefinitions(tools?.tools ?? []);
      setRuntimeSettings(settings);
      if (info) {
        const serverDefinition = await getGraphDefinition().catch(() => null);
        if (serverDefinition) {
          syncedGraphRef.current = {
            signature: graphUploadSignature(serverDefinition, info.id, info.version),
            official: info.official === true,
          };
        }
        if (!preferLocalGraphRef.current) {
          setGraphId(info.id);
          setGraphVersion(info.version);
          if (serverDefinition) setDefinitionText(stringifyJSON(serverDefinition));
          await refreshInitialRequirements().catch((err) => {
            setInitialRequirements(null);
            setInitialRequirementsError(err instanceof Error ? err.message : String(err));
          });
        }
      }
      const loadIdentity = info
        ? { id: info.id || "graph", version: info.version || "1.0" }
        : undefined;
      await refreshRuns(loadIdentity, !preferLocalGraphRef.current).catch(() => undefined);
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

      const signature = graphUploadSignature(definition, currentGraphIdentity.id, currentGraphIdentity.version);
      if (graphUploadRequired(signature, syncedGraphRef.current)) {
        const result = await setGraphDefinition(definition, graphId, graphVersion);
        setGraphInfo(result.graph);
        setGraphId(result.graph.id);
        setGraphVersion(result.graph.version);
        syncedGraphRef.current = {
          signature: graphUploadSignature(definition, result.graph.id, result.graph.version),
          official: result.graph.official === true,
        };
        await refreshInitialRequirements().catch((err) => {
          setInitialRequirements(null);
          setInitialRequirementsError(err instanceof Error ? err.message : String(err));
        });
      }
      await startConfiguredRun(initialState);
    } catch (err) {
      setInitialRequirementsError(err instanceof Error ? err.message : String(err));
      notifyError(err);
    } finally {
      setBusy(false);
    }
  }

  async function saveRuntimeSettings(settings: RuntimeSettingsUpdate): Promise<RuntimeSettings> {
    try {
      const next = await updateRuntimeSettings(settings);
      setRuntimeSettings(next);
      pushToast("info", "Runtime settings updated");
      return next;
    } catch (err) {
      notifyError(err);
      throw err;
    }
  }

  async function publishGraph() {
    setBusy(true);
    setPublishing(true);
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

      const signature = graphUploadSignature(definition, currentGraphIdentity.id, currentGraphIdentity.version);
      if (!graphPublishRequired(signature, syncedGraphRef.current)) {
        pushToast("info", `Already published ${currentGraphIdentity.id}@${currentGraphIdentity.version}`);
        return;
      }

      const requirements = await analyzeInitialStateRequirements(definition, graphId, graphVersion);
      setInitialRequirements(requirements);
      setInitialRequirementsError("");
      if (requirements.unresolved.length > 0) {
        const unresolved = requirements.unresolved.map((item) => item.path).slice(0, 4).join(", ");
        const suffix = requirements.unresolved.length > 4 ? ` (+${requirements.unresolved.length - 4} more)` : "";
        pushToast("error", `Unresolved state requirements: ${unresolved}${suffix}`);
        return;
      }

      const result = await publishGraphDefinition(definition, graphId, graphVersion);
      setGraphInfo(result.graph);
      setGraphId(result.graph.id);
      setGraphVersion(result.graph.version);
      syncedGraphRef.current = {
        signature: graphUploadSignature(definition, result.graph.id, result.graph.version),
        official: result.graph.official === true,
      };
      const session = result.graph.graph_session_id ? ` (${result.graph.graph_session_id})` : "";
      pushToast("info", `Published ${result.graph.id}@${result.graph.version}${session}`);
    } catch (err) {
      setInitialRequirementsError(err instanceof Error ? err.message : String(err));
      notifyError(err);
    } finally {
      setPublishing(false);
      setBusy(false);
    }
  }

  return (
    <WorkbenchShell
      tab={tab}
      streamStatus={streamStatus}
      busy={workbenchBusy}
      publishing={publishing}
      definition={definition}
      runControlMode={runControlMode}
      canResume={canResumeSelectedRun}
      onRun={runGraph}
      onPublish={() => void publishGraph()}
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
          events={displayEvents}
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
          onUpdateRuntimeSettings={saveRuntimeSettings}
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

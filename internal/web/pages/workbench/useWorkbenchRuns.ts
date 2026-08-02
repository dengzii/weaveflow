import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  cancelRun,
  deleteRun,
  getRunInspection,
  listRuns,
  listTriggerInvocations,
  pauseRun,
  resumeRun,
  startRun,
} from "../../api";
import { emitRuntimeEvent } from "../../lib/runtimeEvents";
import { parseJSON } from "../../lib/utils";
import type {
  GraphDefinition,
  RunInterrupt,
  RunRecord,
  RuntimeEvent,
  StepRecord,
  TriggerType,
} from "../../types";
import { useRuntimeEventStream } from "./useRuntimeEventStream";
import {
  pendingUserInputState,
  userInputPromptFromInterrupt,
  type UserInputPrompt,
} from "./userInputModel";
import {
  canResumeRun,
  isActiveRunStatus,
  isTerminalRunStatus,
  markRunResuming,
  matchesGraphIdentity,
  reconcileRunEvents,
  runControlModeFromRun,
  runStatusFromEvent,
  runTriggerTypesFromInvocations,
  selectedRunIDAfterDeletion,
  upsertRunFromEvent,
  type GraphIdentity,
  type RunControlMode,
} from "./workbenchRunModel";

const RUN_STATUS_VISIBLE_STORAGE_KEY = "weaveflow.workbench.runStatus.visible";

type RunNotificationTone = "info" | "warn" | "error";

interface UseWorkbenchRunsOptions {
  graphIdentity: GraphIdentity;
  definition: GraphDefinition | null;
  initialStateText: string;
  onNotify: (tone: RunNotificationTone, message: string) => void;
}

interface WorkbenchRunsController {
  runs: RunRecord[];
  runTriggerTypes: Partial<Record<string, TriggerType>>;
  selectedRunID: string;
  steps: StepRecord[];
  displayEvents: RuntimeEvent[];
  humanPrompt: UserInputPrompt | null;
  humanPromptText: string;
  runStatusVisible: boolean;
  runBusy: boolean;
  graphSwitchLocked: boolean;
  canResumeSelectedRun: boolean;
  runControlMode: RunControlMode;
  streamStatus: ReturnType<typeof useRuntimeEventStream>;
  setHumanPromptText: (value: string) => void;
  selectRun: (runID: string) => void;
  hideRunStatus: () => void;
  toggleRunStatus: () => void;
  resetRunState: () => void;
  refreshRuns: (identity?: GraphIdentity, autoSelect?: boolean) => Promise<void>;
  startConfiguredRun: (initialState: unknown) => Promise<void>;
  pauseSelectedRun: () => Promise<void>;
  cancelSelectedRun: () => Promise<void>;
  deleteRunRecord: (runID: string) => Promise<void>;
  resumeSelectedRun: () => Promise<void>;
  submitUserInputPrompt: () => Promise<void>;
  dismissUserInputPrompt: () => void;
}

export function useWorkbenchRuns({
  graphIdentity,
  definition,
  initialStateText,
  onNotify,
}: UseWorkbenchRunsOptions): WorkbenchRunsController {
  const [runs, setRuns] = useState<RunRecord[]>([]);
  const [runTriggerTypes, setRunTriggerTypes] = useState<Partial<Record<string, TriggerType>>>({});
  const [selectedRunID, setSelectedRunID] = useState("");
  const [steps, setSteps] = useState<StepRecord[]>([]);
  const [storedEvents, setStoredEvents] = useState<RuntimeEvent[]>([]);
  const [liveEvents, setLiveEvents] = useState<RuntimeEvent[]>([]);
  const [runInterrupt, setRunInterrupt] = useState<RunInterrupt | null>(null);
  const [humanPrompt, setHumanPrompt] = useState<UserInputPrompt | null>(null);
  const [humanPromptText, setHumanPromptText] = useState("");
  const [runStatusVisible, setRunStatusVisible] = useState(readStoredRunStatusVisible);
  const [runBusy, setRunBusy] = useState(false);
  const [runLaunchPending, setRunLaunchPending] = useState(false);
  const runsRef = useRef<RunRecord[]>([]);
  const selectedRunIDRef = useRef("");
  const launchPendingRef = useRef(false);
  const launchRunIDRef = useRef("");
  const graphIdentityRef = useRef(graphIdentity);
  const graphRunsRefreshStartedRef = useRef(false);
  const runContextVersionRef = useRef(0);
  const selectedRunRequestRef = useRef(0);
  const ignoredHumanInterruptsRef = useRef<Set<string>>(new Set());
  const humanPromptCheckpointRef = useRef("");

  const clearSelectedRunInspection = useCallback(() => {
    setSteps([]);
    setStoredEvents([]);
    setRunInterrupt(null);
    setHumanPrompt(null);
    setHumanPromptText("");
    humanPromptCheckpointRef.current = "";
  }, []);

  const updateRuns = useCallback((nextRuns: RunRecord[] | ((current: RunRecord[]) => RunRecord[])) => {
    const next = typeof nextRuns === "function" ? nextRuns(runsRef.current) : nextRuns;
    runsRef.current = next;
    setRuns(next);
  }, []);

  const updateSelectedRunID = useCallback((runID: string) => {
    if (selectedRunIDRef.current === runID) return;
    selectedRunIDRef.current = runID;
    selectedRunRequestRef.current += 1;
    setSelectedRunID(runID);
    clearSelectedRunInspection();
  }, [clearSelectedRunInspection]);

  const reportError = useCallback((error: unknown): string => {
    const message = error instanceof Error ? error.message : String(error);
    onNotify("error", message);
    return message;
  }, [onNotify]);

  const selectedRun = useMemo(
    () => runs.find((run) => run.run_id === selectedRunID) ?? null,
    [runs, selectedRunID]
  );
  const displayEvents = useMemo(
    () => reconcileRunEvents(liveEvents, storedEvents, selectedRunID),
    [liveEvents, selectedRunID, storedEvents]
  );
  const canResumeSelectedRun = canResumeRun(selectedRun, runInterrupt);
  const runControlMode = runControlModeFromRun(selectedRun, runInterrupt);
  const graphSwitchLocked =
    runLaunchPending ||
    Boolean(selectedRun && isActiveRunStatus(selectedRun.status)) ||
    runs.some((run) => isActiveRunStatus(run.status) && matchesGraphIdentity(run, graphIdentity));

  useEffect(() => {
    graphIdentityRef.current = graphIdentity;
  }, [graphIdentity]);

  useEffect(() => {
    writeStoredRunStatusVisible(runStatusVisible);
  }, [runStatusVisible]);

  const resetRunState = useCallback(() => {
    runContextVersionRef.current += 1;
    launchPendingRef.current = false;
    launchRunIDRef.current = "";
    ignoredHumanInterruptsRef.current.clear();
    setRunLaunchPending(false);
    setRunBusy(false);
    updateRuns([]);
    updateSelectedRunID("");
    setLiveEvents([]);
    setRunTriggerTypes({});
    clearSelectedRunInspection();
  }, [clearSelectedRunInspection, updateRuns, updateSelectedRunID]);

  const maybeOpenUserInputPrompt = useCallback((interrupt?: RunInterrupt | null) => {
    const prompt = userInputPromptFromInterrupt(interrupt, definition);
    if (!prompt) return;
    if (ignoredHumanInterruptsRef.current.has(prompt.checkpointID)) return;
    if (humanPromptCheckpointRef.current === prompt.checkpointID) return;
    humanPromptCheckpointRef.current = prompt.checkpointID;
    setHumanPrompt(prompt);
    setHumanPromptText("");
  }, [definition]);

  // The pause event can arrive before detail state, so prompt discovery also follows interrupt updates.
  useEffect(() => {
    maybeOpenUserInputPrompt(runInterrupt);
  }, [maybeOpenUserInputPrompt, runInterrupt]);

  const refreshRuns = useCallback(async (
    identity: GraphIdentity = graphIdentityRef.current,
    autoSelect = true
  ) => {
    const contextVersion = runContextVersionRef.current;
    const [nextRuns, triggerInvocations] = await Promise.all([
      listRuns(identity.id),
      listTriggerInvocations(undefined, 500).catch(() => []),
    ]);
    if (runContextVersionRef.current !== contextVersion) return;
    updateRuns(nextRuns ?? []);
    setRunTriggerTypes(runTriggerTypesFromInvocations(triggerInvocations, identity.id));
    if (!autoSelect) return;
    const currentRunID = selectedRunIDRef.current;
    const nextRunID = currentRunID && nextRuns.some((run) => run.run_id === currentRunID)
      ? currentRunID
      : nextRuns.at(-1)?.run_id || "";
    if (nextRunID !== currentRunID) updateSelectedRunID(nextRunID);
  }, [updateRuns, updateSelectedRunID]);

  useEffect(() => {
    if (!graphRunsRefreshStartedRef.current) {
      graphRunsRefreshStartedRef.current = true;
      return;
    }
    void refreshRuns(graphIdentity).catch(reportError);
  }, [graphIdentity.id, refreshRuns, reportError]);

  const refreshSelectedRun = useCallback(async (runID: string) => {
    const contextVersion = runContextVersionRef.current;
    const requestVersion = ++selectedRunRequestRef.current;
    if (!runID) {
      clearSelectedRunInspection();
      return;
    }
    const graphID = runsRef.current.find((run) => run.run_id === runID)?.graph_id || graphIdentityRef.current.id;
    const inspection = await getRunInspection(runID, graphID);
    if (
      runContextVersionRef.current !== contextVersion ||
      selectedRunRequestRef.current !== requestVersion ||
      selectedRunIDRef.current !== runID
    ) {
      return;
    }
    setSteps(inspection.steps);
    setStoredEvents(inspection.events);
    setRunInterrupt(inspection.interrupt ?? null);
    updateRuns((current) => current.map((run) => (run.run_id === inspection.run.run_id ? inspection.run : run)));
  }, [clearSelectedRunInspection, updateRuns]);

  const refreshPausedRun = useCallback(async (
    runID: string,
    options: { openHumanPrompt?: boolean } = {}
  ) => {
    if (!runID) return;
    const contextVersion = runContextVersionRef.current;
    try {
      const graphID = runsRef.current.find((run) => run.run_id === runID)?.graph_id || graphIdentityRef.current.id;
      const inspection = await getRunInspection(runID, graphID);
      if (runContextVersionRef.current !== contextVersion) return;
      updateRuns((current) => {
        const exists = current.some((run) => run.run_id === inspection.run.run_id);
        return exists
          ? current.map((run) => (run.run_id === inspection.run.run_id ? inspection.run : run))
          : [...current, inspection.run];
      });

      const selectedRun = selectedRunIDRef.current;
      const launchRun = launchRunIDRef.current;
      if (selectedRun && selectedRun !== runID && launchRun !== runID) return;

      updateSelectedRunID(runID);
      setRunStatusVisible(true);
      setSteps(inspection.steps);
      setStoredEvents(inspection.events);
      setRunInterrupt(inspection.interrupt ?? null);
      if (options.openHumanPrompt) maybeOpenUserInputPrompt(inspection.interrupt ?? null);
    } catch (error) {
      reportError(error);
    }
  }, [maybeOpenUserInputPrompt, reportError, updateRuns, updateSelectedRunID]);

  const handleRuntimeEvent = useCallback((event: RuntimeEvent) => {
    const selectedRun = selectedRunIDRef.current;
    const launchRun = launchRunIDRef.current;
    const launchMatches = launchPendingRef.current && (!launchRun || event.run_id === launchRun);
    const shouldTrack =
      !event.run_id ||
      (selectedRun ? event.run_id === selectedRun || event.run_id === launchRun : launchMatches);
    if (!shouldTrack) return;

    emitRuntimeEvent(event);
    setLiveEvents((current) => {
      const retainRunID = event.run_id || selectedRun || launchRun;
      const next = [event, ...current];
      return retainRunID ? next.filter((item) => !item.run_id || item.run_id === retainRunID) : next;
    });
    const nextStatus = runStatusFromEvent(event.type);
    if (event.run_id) {
      updateRuns((current) => upsertRunFromEvent(current, event, nextStatus, graphIdentityRef.current));
    }
    if (event.run_id && (event.type === "run.created" || (launchPendingRef.current && !launchRunIDRef.current))) {
      launchRunIDRef.current = event.run_id;
      if (launchPendingRef.current || !selectedRunIDRef.current) {
        updateSelectedRunID(event.run_id);
        setRunStatusVisible(true);
      }
    }
    if (event.run_id && event.run_id === launchRunIDRef.current && nextStatus && isTerminalRunStatus(nextStatus)) {
      launchPendingRef.current = false;
      setRunLaunchPending(false);
    }
    if (event.run_id && event.type === "run.paused") {
      void refreshPausedRun(event.run_id, { openHumanPrompt: true });
    }
  }, [refreshPausedRun, updateRuns, updateSelectedRunID]);
  const streamStatus = useRuntimeEventStream(handleRuntimeEvent);

  useEffect(() => {
    void refreshSelectedRun(selectedRunID).catch(reportError);
  }, [refreshSelectedRun, reportError, selectedRunID]);

  const selectRun = useCallback((runID: string) => {
    updateSelectedRunID(runID);
    setRunStatusVisible(true);
  }, [updateSelectedRunID]);

  const startConfiguredRun = useCallback(async (initialState: unknown) => {
    setRunBusy(true);
    const runContextVersion = ++runContextVersionRef.current;
    launchPendingRef.current = true;
    launchRunIDRef.current = "";
    setRunLaunchPending(true);
    updateSelectedRunID("");
    setLiveEvents([]);
    ignoredHumanInterruptsRef.current.clear();
    clearSelectedRunInspection();
    setRunStatusVisible(true);
    try {
      const result = await startRun(initialState);
      launchPendingRef.current = false;
      setRunLaunchPending(false);
      updateSelectedRunID(result.run.run_id);
      setRunInterrupt(result.interrupt ?? null);
      await refreshRuns(graphIdentityRef.current);
      await refreshSelectedRun(result.run.run_id);
      onNotify("info", `Run ${result.run.status}: ${result.run.run_id}`);
    } catch (error) {
      launchPendingRef.current = false;
      setRunLaunchPending(false);
      reportError(error);
    } finally {
      if (runContextVersionRef.current === runContextVersion) setRunBusy(false);
    }
  }, [clearSelectedRunInspection, onNotify, refreshRuns, refreshSelectedRun, reportError, updateSelectedRunID]);

  const controlSelectedRun = useCallback(async (kind: "pause" | "cancel") => {
    const runID = selectedRunIDRef.current;
    if (!runID) return;
    const selected = runsRef.current.find((run) => run.run_id === runID) ?? null;
    setRunBusy(true);
    try {
      const run = await (kind === "pause"
        ? pauseRun(runID)
        : cancelRun(runID, selected?.graph_id || graphIdentityRef.current.id));
      updateRuns((current) => current.map((item) => (item.run_id === run.run_id ? run : item)));
      updateSelectedRunID(run.run_id);
      await refreshRuns(graphIdentityRef.current);
      await refreshSelectedRun(run.run_id);
      onNotify(
        kind === "pause" ? "warn" : "info",
        `${kind === "pause" ? "Run paused" : "Run stopped"}: ${run.run_id}`
      );
    } catch (error) {
      reportError(error);
    } finally {
      setRunBusy(false);
    }
  }, [onNotify, refreshRuns, refreshSelectedRun, reportError, updateRuns, updateSelectedRunID]);

  const pauseSelectedRun = useCallback(
    () => controlSelectedRun("pause"),
    [controlSelectedRun]
  );
  const cancelSelectedRun = useCallback(
    () => controlSelectedRun("cancel"),
    [controlSelectedRun]
  );

  const deleteRunRecord = useCallback(async (runID: string) => {
    const target = runsRef.current.find((run) => run.run_id === runID) ?? null;
    if (!target || isActiveRunStatus(target.status)) return;
    if (!window.confirm(`Delete run ${runID}?`)) return;

    setRunBusy(true);
    try {
      await deleteRun(runID, target.graph_id || graphIdentityRef.current.id);
      const remainingRuns = runsRef.current.filter((run) => run.run_id !== runID);
      const currentRunID = selectedRunIDRef.current;
      const wasSelected = currentRunID === runID;
      const nextRunID = selectedRunIDAfterDeletion(runsRef.current, runID, currentRunID);
      updateRuns(remainingRuns);
      updateSelectedRunID(nextRunID);
      if (wasSelected || !nextRunID) {
        setLiveEvents([]);
        clearSelectedRunInspection();
      }
      if (launchRunIDRef.current === runID) launchRunIDRef.current = "";
      await refreshRuns(graphIdentityRef.current, false);
      if (nextRunID) await refreshSelectedRun(nextRunID);
      onNotify("info", `Run deleted: ${runID}`);
    } catch (error) {
      reportError(error);
    } finally {
      setRunBusy(false);
    }
  }, [clearSelectedRunInspection, onNotify, refreshRuns, refreshSelectedRun, reportError, updateRuns, updateSelectedRunID]);

  const refreshAfterResumeFailure = useCallback(async (runID: string) => {
    try {
      await refreshSelectedRun(runID);
    } catch {
      // Preserve the original resume error; a later event or selection refresh can reconcile state.
    }
  }, [refreshSelectedRun]);

  const resumeSelectedRun = useCallback(async () => {
    const runID = selectedRunIDRef.current;
    if (!runID) return;
    const prompt = userInputPromptFromInterrupt(runInterrupt, definition);
    if (prompt) {
      humanPromptCheckpointRef.current = prompt.checkpointID;
      setHumanPrompt(prompt);
      setHumanPromptText("");
      setRunStatusVisible(true);
      onNotify("warn", "User input required to resume");
      return;
    }

    setRunBusy(true);
    try {
      const input = parseJSON<unknown>(initialStateText);
      const runContextVersion = runContextVersionRef.current;
      updateRuns((current) => markRunResuming(current, runID, new Date().toISOString()));
      setRunInterrupt(null);
      const result = await resumeRun(runID, input);
      updateSelectedRunID(result.run.run_id);
      setRunInterrupt(result.interrupt ?? null);
      setRunStatusVisible(true);
      await refreshRuns(graphIdentityRef.current);
      await refreshSelectedRun(result.run.run_id);
      if (runContextVersionRef.current === runContextVersion) {
        onNotify("info", `Run ${result.run.status}: ${result.run.run_id}`);
      }
    } catch (error) {
      reportError(error);
      await refreshAfterResumeFailure(runID);
    } finally {
      setRunBusy(false);
    }
  }, [definition, initialStateText, onNotify, refreshAfterResumeFailure, refreshRuns, refreshSelectedRun, reportError, runInterrupt, updateRuns, updateSelectedRunID]);

  const submitUserInputPrompt = useCallback(async () => {
    if (!humanPrompt) return;
    const prompt = humanPrompt;
    const text = humanPromptText.trim();
    if (!text) return;
    ignoredHumanInterruptsRef.current.add(prompt.checkpointID);
    setHumanPrompt(null);
    setHumanPromptText("");
    setRunInterrupt(null);
    setRunBusy(true);
    try {
      const runContextVersion = runContextVersionRef.current;
      updateRuns((current) => markRunResuming(current, prompt.runID, new Date().toISOString()));
      const result = await resumeRun(prompt.runID, pendingUserInputState(prompt.statePath, text));
      updateSelectedRunID(result.run.run_id);
      setRunInterrupt(result.interrupt ?? null);
      setRunStatusVisible(true);
      await refreshRuns(graphIdentityRef.current);
      await refreshSelectedRun(result.run.run_id);
      if (runContextVersionRef.current === runContextVersion) {
        onNotify("info", `Run ${result.run.status}: ${result.run.run_id}`);
      }
    } catch (error) {
      ignoredHumanInterruptsRef.current.delete(prompt.checkpointID);
      humanPromptCheckpointRef.current = prompt.checkpointID;
      setHumanPrompt(prompt);
      setHumanPromptText(text);
      reportError(error);
      await refreshAfterResumeFailure(prompt.runID);
    } finally {
      setRunBusy(false);
    }
  }, [humanPrompt, humanPromptText, onNotify, refreshAfterResumeFailure, refreshRuns, refreshSelectedRun, reportError, updateRuns, updateSelectedRunID]);

  const dismissUserInputPrompt = useCallback(() => {
    if (humanPrompt?.checkpointID) ignoredHumanInterruptsRef.current.add(humanPrompt.checkpointID);
    setHumanPrompt(null);
    setHumanPromptText("");
    humanPromptCheckpointRef.current = "";
  }, [humanPrompt]);

  const hideRunStatus = useCallback(() => setRunStatusVisible(false), []);
  const toggleRunStatus = useCallback(() => setRunStatusVisible((visible) => !visible), []);

  return {
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
  };
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
    // Storage can be unavailable; keep the session state in React.
  }
}

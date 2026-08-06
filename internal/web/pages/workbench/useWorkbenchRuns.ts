import { startTransition, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  cancelRun,
  deleteRun,
  getRunInspection,
  listEvents,
  listRuns,
  listTriggerInvocations,
  pauseRun,
  resumeRun,
  startRun,
} from "../../api";
import { emitRuntimeEvent } from "../../lib/runtimeEvents";
import { parseJSON } from "../../lib/utils";
import type {
  CheckpointRecord,
  GraphDefinition,
  RunInterrupt,
  RunInspection,
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
  mergeLiveRuntimeEvents,
  mergeRefreshedRuns,
  mergeStoredRuntimeEvents,
  reconcileRunEvents,
  runListEventAction,
  runControlModeFromRun,
  runStatusFromEvent,
  runTriggerTypesFromInvocations,
  selectedRunIDAfterDeletion,
  upsertInspectedRun,
  upsertRunFromEvent,
  type GraphIdentity,
  type RunControlMode,
} from "./workbenchRunModel";

const RUN_STATUS_VISIBLE_STORAGE_KEY = "weaveflow.workbench.runStatus.visible";
const LIVE_EVENT_FLUSH_INTERVAL_MS = 80;
const MAX_CACHED_RUN_INSPECTIONS = 6;

type RunNotificationTone = "info" | "warn" | "error";

function rememberRunInspection(cache: Map<string, RunInspection>, inspection: RunInspection): void {
  cache.delete(inspection.run.run_id);
  cache.set(inspection.run.run_id, inspection);
  while (cache.size > MAX_CACHED_RUN_INSPECTIONS) {
    const oldestRunID = cache.keys().next().value;
    if (typeof oldestRunID !== "string") break;
    cache.delete(oldestRunID);
  }
}

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
  runInspectionLoading: boolean;
  steps: StepRecord[];
  checkpoints: CheckpointRecord[];
  displayEvents: RuntimeEvent[];
  hasOlderEvents: boolean;
  olderEventsLoading: boolean;
  humanPrompt: UserInputPrompt | null;
  humanPromptText: string;
  runStatusVisible: boolean;
  runBusy: boolean;
  runLaunchPending: boolean;
  graphSwitchLocked: boolean;
  canResumeSelectedRun: boolean;
  runControlMode: RunControlMode;
  streamStatus: ReturnType<typeof useRuntimeEventStream>;
  setHumanPromptText: (value: string) => void;
  selectRun: (runID: string) => void;
  loadOlderEvents: () => Promise<void>;
  hideRunStatus: () => void;
  toggleRunStatus: () => void;
  resetRunState: () => void;
  refreshRuns: (identity?: GraphIdentity, autoSelect?: boolean) => Promise<void>;
  startConfiguredRun: (initialState: unknown, identity?: GraphIdentity) => Promise<void>;
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
  const [runInspectionLoading, setRunInspectionLoading] = useState(false);
  const [steps, setSteps] = useState<StepRecord[]>([]);
  const [checkpoints, setCheckpoints] = useState<CheckpointRecord[]>([]);
  const [storedEvents, setStoredEvents] = useState<RuntimeEvent[]>([]);
  const [liveEvents, setLiveEvents] = useState<RuntimeEvent[]>([]);
  const [nextEventCursor, setNextEventCursor] = useState("");
  const [olderEventsLoading, setOlderEventsLoading] = useState(false);
  const [runInterrupt, setRunInterrupt] = useState<RunInterrupt | null>(null);
  const [humanPrompt, setHumanPrompt] = useState<UserInputPrompt | null>(null);
  const [humanPromptText, setHumanPromptText] = useState("");
  const [runStatusVisible, setRunStatusVisible] = useState(readStoredRunStatusVisible);
  const [runBusy, setRunBusy] = useState(false);
  const [runLaunchPending, setRunLaunchPending] = useState(false);
  const runsRef = useRef<RunRecord[]>([]);
  const runsRefreshStartedRef = useRef(0);
  const runBusyRef = useRef(false);
  const selectedRunIDRef = useRef("");
  const userRunSelectionVersionRef = useRef(0);
  const launchPendingRef = useRef(false);
  const launchRunIDRef = useRef("");
  const graphIdentityRef = useRef(graphIdentity);
  const graphRunsRefreshStartedRef = useRef(false);
  const runContextVersionRef = useRef(0);
  const selectedRunRequestRef = useRef(0);
  const ignoredHumanInterruptsRef = useRef<Set<string>>(new Set());
  const humanPromptCheckpointRef = useRef("");
  const pendingLiveEventsRef = useRef<RuntimeEvent[]>([]);
  const liveEventsFlushTimerRef = useRef<number | null>(null);
  const liveEventsVersionRef = useRef(0);
  const eventCursorRef = useRef("");
  const eventPageLoadingRef = useRef(false);
  const eventPageRequestRef = useRef(0);
  const eventRunRefreshRequestedRef = useRef(false);
  const eventRunRefreshRunningRef = useRef(false);
  const runInspectionCacheRef = useRef<Map<string, RunInspection>>(new Map());

  const discardPendingLiveEvents = useCallback(() => {
    pendingLiveEventsRef.current = [];
    if (liveEventsFlushTimerRef.current !== null) {
      window.clearTimeout(liveEventsFlushTimerRef.current);
      liveEventsFlushTimerRef.current = null;
    }
  }, []);

  const clearLiveEvents = useCallback(() => {
    liveEventsVersionRef.current += 1;
    discardPendingLiveEvents();
    setLiveEvents([]);
  }, [discardPendingLiveEvents]);

  const flushPendingLiveEvents = useCallback(() => {
    liveEventsFlushTimerRef.current = null;
    const incoming = pendingLiveEventsRef.current;
    pendingLiveEventsRef.current = [];
    if (incoming.length === 0) return;
    const contextVersion = runContextVersionRef.current;
    const liveEventsVersion = liveEventsVersionRef.current;
    const retainRunID = incoming.at(-1)?.run_id || selectedRunIDRef.current || launchRunIDRef.current;
    startTransition(() => {
      setLiveEvents((current) =>
        runContextVersionRef.current === contextVersion && liveEventsVersionRef.current === liveEventsVersion
          ? mergeLiveRuntimeEvents(current, incoming, retainRunID)
          : current
      );
    });
  }, []);

  const enqueueLiveEvent = useCallback((event: RuntimeEvent) => {
    pendingLiveEventsRef.current.push(event);
    if (liveEventsFlushTimerRef.current !== null) return;
    liveEventsFlushTimerRef.current = window.setTimeout(
      flushPendingLiveEvents,
      LIVE_EVENT_FLUSH_INTERVAL_MS
    );
  }, [flushPendingLiveEvents]);

  const clearSelectedRunInspection = useCallback(() => {
    eventPageRequestRef.current += 1;
    eventCursorRef.current = "";
    eventPageLoadingRef.current = false;
    setSteps([]);
    setCheckpoints([]);
    setStoredEvents([]);
    setNextEventCursor("");
    setOlderEventsLoading(false);
    setRunInterrupt(null);
    setHumanPrompt(null);
    setHumanPromptText("");
    humanPromptCheckpointRef.current = "";
  }, []);

  const replaceStoredEventPage = useCallback((events: RuntimeEvent[], nextCursor: string) => {
    eventPageRequestRef.current += 1;
    eventCursorRef.current = nextCursor;
    eventPageLoadingRef.current = false;
    setStoredEvents(events);
    setNextEventCursor(nextCursor);
    setOlderEventsLoading(false);
  }, []);

  const applyRuns = useCallback((nextRuns: RunRecord[] | ((current: RunRecord[]) => RunRecord[])) => {
    const next = typeof nextRuns === "function" ? nextRuns(runsRef.current) : nextRuns;
    runsRef.current = next;
    setRuns(next);
  }, []);

  const updateRuns = useCallback((nextRuns: RunRecord[] | ((current: RunRecord[]) => RunRecord[])) => {
    const current = runsRef.current;
    const next = typeof nextRuns === "function" ? nextRuns(current) : nextRuns;
    if (next === current) return;
    applyRuns(next);
  }, [applyRuns]);

  const applyRunInspection = useCallback((inspection: RunInspection) => {
    setSteps(inspection.steps);
    setCheckpoints((current) => mergeFetchedCheckpoints(current, inspection.checkpoints));
    replaceStoredEventPage(inspection.events, inspection.event_cursor);
    setRunInterrupt(inspection.interrupt ?? null);
    updateRuns((current) => upsertInspectedRun(current, inspection.run));
  }, [replaceStoredEventPage, updateRuns]);

  const updateSelectedRunID = useCallback((runID: string) => {
    if (selectedRunIDRef.current === runID) return;
    selectedRunIDRef.current = runID;
    selectedRunRequestRef.current += 1;
    setSelectedRunID(runID);
    setRunInspectionLoading(Boolean(runID));
    clearSelectedRunInspection();
  }, [clearSelectedRunInspection]);

  const reportError = useCallback((error: unknown): string => {
    const message = error instanceof Error ? error.message : String(error);
    onNotify("error", message);
    return message;
  }, [onNotify]);

  const beginRunOperation = useCallback(({
    allowDuringLaunch = false,
  }: { allowDuringLaunch?: boolean } = {}): boolean => {
    if (runBusyRef.current || (launchPendingRef.current && !allowDuringLaunch)) return false;
    runBusyRef.current = true;
    setRunBusy(true);
    return true;
  }, []);

  const finishRunOperation = useCallback((contextVersion: number) => {
    if (runContextVersionRef.current !== contextVersion) return;
    runBusyRef.current = false;
    setRunBusy(false);
  }, []);

  const isRunSelectionCurrent = useCallback(
    (runID: string): boolean => selectedRunIDRef.current === runID,
    []
  );

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

  useEffect(() => discardPendingLiveEvents, [discardPendingLiveEvents]);

  const resetRunState = useCallback(() => {
    runContextVersionRef.current += 1;
    runBusyRef.current = false;
    launchPendingRef.current = false;
    launchRunIDRef.current = "";
    ignoredHumanInterruptsRef.current.clear();
    runInspectionCacheRef.current.clear();
    setRunLaunchPending(false);
    setRunBusy(false);
    setRunInspectionLoading(false);
    updateRuns([]);
    updateSelectedRunID("");
    clearLiveEvents();
    setRunTriggerTypes({});
    clearSelectedRunInspection();
  }, [clearLiveEvents, clearSelectedRunInspection, updateRuns, updateSelectedRunID]);

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
    const runsAtStart = runsRef.current;
    const refreshRequest = ++runsRefreshStartedRef.current;
    try {
      const [nextRuns, triggerInvocations] = await Promise.all([
        listRuns(identity.id),
        listTriggerInvocations(undefined, 500).catch(() => null),
      ]);
      if (
        runContextVersionRef.current !== contextVersion ||
        refreshRequest !== runsRefreshStartedRef.current
      ) {
        return;
      }
      const mergedRuns = mergeRefreshedRuns(
        runsRef.current,
        nextRuns ?? [],
        runsAtStart
      );
      applyRuns(mergedRuns);
      if (triggerInvocations) {
        setRunTriggerTypes(runTriggerTypesFromInvocations(triggerInvocations, identity.id));
      }
      if (!autoSelect) return;
      const currentRunID = selectedRunIDRef.current;
      const nextRunID = currentRunID && mergedRuns.some((run) => run.run_id === currentRunID)
        ? currentRunID
        : mergedRuns.at(-1)?.run_id || "";
      if (nextRunID !== currentRunID) updateSelectedRunID(nextRunID);
    } catch (error) {
      if (
        runContextVersionRef.current !== contextVersion ||
        refreshRequest !== runsRefreshStartedRef.current
      ) {
        return;
      }
      throw error;
    }
  }, [applyRuns, updateSelectedRunID]);

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
      setRunInspectionLoading(false);
      return;
    }
    const graphID = runsRef.current.find((run) => run.run_id === runID)?.graph_id || graphIdentityRef.current.id;
    let inspection: RunInspection;
    try {
      inspection = await getRunInspection(runID, graphID);
    } catch (error) {
      if (
        runContextVersionRef.current !== contextVersion ||
        selectedRunRequestRef.current !== requestVersion ||
        selectedRunIDRef.current !== runID
      ) {
        return;
      }
      setRunInspectionLoading(false);
      throw error;
    }
    if (
      runContextVersionRef.current !== contextVersion ||
      selectedRunRequestRef.current !== requestVersion ||
      selectedRunIDRef.current !== runID
    ) {
      return;
    }
    rememberRunInspection(runInspectionCacheRef.current, inspection);
    applyRunInspection(inspection);
    setRunInspectionLoading(false);
  }, [applyRunInspection, clearSelectedRunInspection]);

  const loadOlderEvents = useCallback(async () => {
    const runID = selectedRunIDRef.current;
    const cursor = eventCursorRef.current;
    if (!runID || !cursor || eventPageLoadingRef.current) return;

    const contextVersion = runContextVersionRef.current;
    const selectedRunRequestVersion = selectedRunRequestRef.current;
    const pageRequestVersion = ++eventPageRequestRef.current;
    const graphID = runsRef.current.find((run) => run.run_id === runID)?.graph_id || graphIdentityRef.current.id;
    eventPageLoadingRef.current = true;
    setOlderEventsLoading(true);
    try {
      const page = await listEvents(runID, graphID, cursor);
      if (
        runContextVersionRef.current !== contextVersion ||
        selectedRunRequestRef.current !== selectedRunRequestVersion ||
        eventPageRequestRef.current !== pageRequestVersion ||
        selectedRunIDRef.current !== runID
      ) {
        return;
      }
      setStoredEvents((current) => mergeStoredRuntimeEvents(current, page.items));
      eventCursorRef.current = page.next_cursor;
      setNextEventCursor(page.next_cursor);
    } catch (error) {
      if (eventPageRequestRef.current === pageRequestVersion) reportError(error);
    } finally {
      if (eventPageRequestRef.current === pageRequestVersion) {
        eventPageLoadingRef.current = false;
        setOlderEventsLoading(false);
      }
    }
  }, [reportError]);

  const requestEventRunRefresh = useCallback(() => {
    eventRunRefreshRequestedRef.current = true;
    if (eventRunRefreshRunningRef.current) return;

    eventRunRefreshRunningRef.current = true;
    void (async () => {
      try {
        while (eventRunRefreshRequestedRef.current) {
          eventRunRefreshRequestedRef.current = false;
          try {
            await refreshRuns(graphIdentityRef.current, false);
          } catch (error) {
            reportError(error);
          }
        }
      } finally {
        eventRunRefreshRunningRef.current = false;
      }
    })();
  }, [refreshRuns, reportError]);

  const handleRuntimeEvent = useCallback((event: RuntimeEvent) => {
    const listAction = runListEventAction(runsRef.current, event);
    const nextStatus = runStatusFromEvent(event.type);
    if (listAction === "update") {
      updateRuns((current) => upsertRunFromEvent(current, event, nextStatus, graphIdentityRef.current));
    } else if (listAction === "refresh") {
      requestEventRunRefresh();
    }

    const selectedRun = selectedRunIDRef.current;
    const launchRun = launchRunIDRef.current;
    const launchMatches = launchPendingRef.current && (!launchRun || event.run_id === launchRun);
    const shouldTrack = !event.run_id || (selectedRun ? event.run_id === selectedRun : launchMatches);
    if (!shouldTrack) return;

    emitRuntimeEvent(event);
    enqueueLiveEvent(event);
    const checkpoint = checkpointRecordFromEvent(event);
    if (checkpoint) {
      setCheckpoints((current) => upsertCheckpoint(current, checkpoint));
    }
    if (event.run_id && listAction !== "update") {
      updateRuns((current) => upsertRunFromEvent(current, event, nextStatus, graphIdentityRef.current));
    }
    if (
      event.run_id &&
      launchPendingRef.current &&
      (event.type === "run.created" || !launchRunIDRef.current)
    ) {
      launchRunIDRef.current = event.run_id;
      if (!selectedRunIDRef.current) {
        updateSelectedRunID(event.run_id);
        setRunStatusVisible(true);
      }
    }
    if (event.run_id && event.run_id === launchRunIDRef.current && nextStatus && isTerminalRunStatus(nextStatus)) {
      launchPendingRef.current = false;
      launchRunIDRef.current = "";
      setRunLaunchPending(false);
    }
    if (event.run_id && event.type === "run.paused") {
      void refreshSelectedRun(event.run_id).catch(reportError);
    }
  }, [enqueueLiveEvent, refreshSelectedRun, reportError, requestEventRunRefresh, updateRuns, updateSelectedRunID]);
  const streamStatus = useRuntimeEventStream(handleRuntimeEvent);

  useEffect(() => {
    if (streamStatus !== "connected") return;
    requestEventRunRefresh();
    const runID = selectedRunIDRef.current;
    if (runID) void refreshSelectedRun(runID).catch(reportError);
  }, [refreshSelectedRun, reportError, requestEventRunRefresh, streamStatus]);

  useEffect(() => {
    void refreshSelectedRun(selectedRunID).catch(reportError);
  }, [refreshSelectedRun, reportError, selectedRunID]);

  const selectRun = useCallback((runID: string) => {
    userRunSelectionVersionRef.current += 1;
    updateSelectedRunID(runID);
    const cachedInspection = runInspectionCacheRef.current.get(runID);
    if (cachedInspection) {
      applyRunInspection(cachedInspection);
      setRunInspectionLoading(false);
    }
    setRunStatusVisible(true);
  }, [applyRunInspection, updateSelectedRunID]);

  const startConfiguredRun = useCallback(async (
    initialState: unknown,
    identity: GraphIdentity = graphIdentityRef.current
  ) => {
    if (launchPendingRef.current || runBusyRef.current) return;
    const userSelectionVersion = userRunSelectionVersionRef.current;
    graphIdentityRef.current = identity;
    const runContextVersion = ++runContextVersionRef.current;
    launchPendingRef.current = true;
    launchRunIDRef.current = "";
    setRunLaunchPending(true);
    updateSelectedRunID("");
    clearLiveEvents();
    ignoredHumanInterruptsRef.current.clear();
    clearSelectedRunInspection();
    setRunStatusVisible(true);
    try {
      const result = await startRun(initialState);
      if (runContextVersionRef.current !== runContextVersion) return;
      launchPendingRef.current = false;
      launchRunIDRef.current = "";
      setRunLaunchPending(false);
      if (userRunSelectionVersionRef.current === userSelectionVersion) {
        updateSelectedRunID(result.run.run_id);
        setRunInterrupt(result.interrupt ?? null);
      }
      await refreshRuns(identity);
      if (runContextVersionRef.current !== runContextVersion) return;
      if (isRunSelectionCurrent(result.run.run_id)) {
        await refreshSelectedRun(result.run.run_id);
      }
      if (runContextVersionRef.current !== runContextVersion) return;
      onNotify("info", `Run ${result.run.status}: ${result.run.run_id}`);
    } catch (error) {
      if (runContextVersionRef.current !== runContextVersion) return;
      launchPendingRef.current = false;
      launchRunIDRef.current = "";
      setRunLaunchPending(false);
      reportError(error);
    }
  }, [clearLiveEvents, clearSelectedRunInspection, isRunSelectionCurrent, onNotify, refreshRuns, refreshSelectedRun, reportError, updateSelectedRunID]);

  const controlSelectedRun = useCallback(async (kind: "pause" | "cancel") => {
    const runID = selectedRunIDRef.current;
    if (!runID || !beginRunOperation({ allowDuringLaunch: true })) return;
    const runContextVersion = runContextVersionRef.current;
    const selected = runsRef.current.find((run) => run.run_id === runID) ?? null;
    try {
      const run = await (kind === "pause"
        ? pauseRun(runID, selected?.graph_id || graphIdentityRef.current.id)
        : cancelRun(runID, selected?.graph_id || graphIdentityRef.current.id));
      if (runContextVersionRef.current !== runContextVersion) return;
      updateRuns((current) => upsertInspectedRun(current, run));
      await refreshRuns(graphIdentityRef.current);
      if (runContextVersionRef.current !== runContextVersion) return;
      if (isRunSelectionCurrent(run.run_id)) {
        await refreshSelectedRun(run.run_id);
      }
      if (runContextVersionRef.current !== runContextVersion) return;
      onNotify(
        kind === "pause" ? "warn" : "info",
        `${kind === "pause" ? "Run paused" : "Run stopped"}: ${run.run_id}`
      );
    } catch (error) {
      if (runContextVersionRef.current === runContextVersion) reportError(error);
    } finally {
      finishRunOperation(runContextVersion);
    }
  }, [beginRunOperation, finishRunOperation, isRunSelectionCurrent, onNotify, refreshRuns, refreshSelectedRun, reportError, updateRuns]);

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
    if (!beginRunOperation()) return;

    const runContextVersion = runContextVersionRef.current;
    try {
      await deleteRun(runID, target.graph_id || graphIdentityRef.current.id);
      if (runContextVersionRef.current !== runContextVersion) return;
      const remainingRuns = runsRef.current.filter((run) => run.run_id !== runID);
      const currentRunID = selectedRunIDRef.current;
      const wasSelected = currentRunID === runID;
      const nextRunID = selectedRunIDAfterDeletion(runsRef.current, runID, currentRunID);
      updateRuns(remainingRuns);
      updateSelectedRunID(nextRunID);
      if (wasSelected || !nextRunID) {
        clearLiveEvents();
        clearSelectedRunInspection();
      }
      if (launchRunIDRef.current === runID) launchRunIDRef.current = "";
      await refreshRuns(graphIdentityRef.current, false);
      if (runContextVersionRef.current !== runContextVersion) return;
      if (nextRunID) await refreshSelectedRun(nextRunID);
      if (runContextVersionRef.current !== runContextVersion) return;
      onNotify("info", `Run deleted: ${runID}`);
    } catch (error) {
      if (runContextVersionRef.current === runContextVersion) reportError(error);
    } finally {
      finishRunOperation(runContextVersion);
    }
  }, [beginRunOperation, clearLiveEvents, clearSelectedRunInspection, finishRunOperation, onNotify, refreshRuns, refreshSelectedRun, reportError, updateRuns, updateSelectedRunID]);

  const refreshAfterResumeFailure = useCallback(async (runID: string, identity: GraphIdentity) => {
    try {
      await refreshRuns(identity, false);
      if (selectedRunIDRef.current === runID) await refreshSelectedRun(runID);
    } catch {
      // Preserve the original resume error; a later event or selection refresh can reconcile state.
    }
  }, [refreshRuns, refreshSelectedRun]);

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
    if (!beginRunOperation()) return;

    const runContextVersion = runContextVersionRef.current;
    const selected = runsRef.current.find((run) => run.run_id === runID) ?? null;
    const identity = {
      id: selected?.graph_id || graphIdentityRef.current.id,
      version: selected?.graph_version || graphIdentityRef.current.version,
    };
    try {
      const input = parseJSON<unknown>(initialStateText);
      updateRuns((current) => markRunResuming(current, runID));
      setRunInterrupt(null);
      const result = await resumeRun(runID, input, identity.id);
      if (runContextVersionRef.current !== runContextVersion) return;
      updateRuns((current) => upsertInspectedRun(current, result.run));
      if (isRunSelectionCurrent(result.run.run_id)) {
        setRunInterrupt(result.interrupt ?? null);
        setRunStatusVisible(true);
      }
      await refreshRuns(identity);
      if (runContextVersionRef.current !== runContextVersion) return;
      if (isRunSelectionCurrent(result.run.run_id)) {
        await refreshSelectedRun(result.run.run_id);
      }
      if (runContextVersionRef.current !== runContextVersion) return;
      onNotify("info", `Run ${result.run.status}: ${result.run.run_id}`);
    } catch (error) {
      if (runContextVersionRef.current !== runContextVersion) return;
      reportError(error);
      await refreshAfterResumeFailure(runID, identity);
    } finally {
      finishRunOperation(runContextVersion);
    }
  }, [beginRunOperation, definition, finishRunOperation, initialStateText, isRunSelectionCurrent, onNotify, refreshAfterResumeFailure, refreshRuns, refreshSelectedRun, reportError, runInterrupt, updateRuns]);

  const submitUserInputPrompt = useCallback(async () => {
    if (!humanPrompt) return;
    const prompt = humanPrompt;
    const text = humanPromptText.trim();
    if (!text) return;
    if (!beginRunOperation()) return;
    const selected = runsRef.current.find((run) => run.run_id === prompt.runID) ?? null;
    const identity = {
      id: selected?.graph_id || graphIdentityRef.current.id,
      version: selected?.graph_version || graphIdentityRef.current.version,
    };
    ignoredHumanInterruptsRef.current.add(prompt.checkpointID);
    setHumanPrompt(null);
    setHumanPromptText("");
    setRunInterrupt(null);
    const runContextVersion = runContextVersionRef.current;
    try {
      updateRuns((current) => markRunResuming(current, prompt.runID));
      const result = await resumeRun(
        prompt.runID,
        pendingUserInputState(prompt.statePath, text),
        identity.id
      );
      if (runContextVersionRef.current !== runContextVersion) return;
      updateRuns((current) => upsertInspectedRun(current, result.run));
      if (isRunSelectionCurrent(result.run.run_id)) {
        setRunInterrupt(result.interrupt ?? null);
        setRunStatusVisible(true);
      }
      await refreshRuns(identity);
      if (runContextVersionRef.current !== runContextVersion) return;
      if (isRunSelectionCurrent(result.run.run_id)) {
        await refreshSelectedRun(result.run.run_id);
      }
      if (runContextVersionRef.current !== runContextVersion) return;
      onNotify("info", `Run ${result.run.status}: ${result.run.run_id}`);
    } catch (error) {
      if (runContextVersionRef.current !== runContextVersion) return;
      ignoredHumanInterruptsRef.current.delete(prompt.checkpointID);
      humanPromptCheckpointRef.current = prompt.checkpointID;
      setHumanPrompt(prompt);
      setHumanPromptText(text);
      reportError(error);
      await refreshAfterResumeFailure(prompt.runID, identity);
    } finally {
      finishRunOperation(runContextVersion);
    }
  }, [beginRunOperation, finishRunOperation, humanPrompt, humanPromptText, isRunSelectionCurrent, onNotify, refreshAfterResumeFailure, refreshRuns, refreshSelectedRun, reportError, updateRuns]);

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
    runInspectionLoading,
    steps,
    checkpoints,
    displayEvents,
    hasOlderEvents: Boolean(nextEventCursor),
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
  };
}

function checkpointRecordFromEvent(event: RuntimeEvent): CheckpointRecord | null {
  if (event.type !== "checkpoint.created" || !event.run_id || !isRecord(event.payload)) return null;
  const checkpointID = stringField(event.payload, "checkpoint_id");
  const stage = stringField(event.payload, "stage");
  if (!checkpointID || !stage) return null;
  return {
    checkpoint_id: checkpointID,
    run_id: event.run_id,
    step_id: event.step_id ?? "",
    node_id: event.node_id ?? "",
    stage,
    state_codec: "",
    state_version: "",
    created_at: event.timestamp,
  };
}

function upsertCheckpoint(current: CheckpointRecord[], checkpoint: CheckpointRecord): CheckpointRecord[] {
  const existingIndex = current.findIndex((item) => item.checkpoint_id === checkpoint.checkpoint_id);
  const next = existingIndex >= 0
    ? current.map((item, index) => (index === existingIndex ? { ...checkpoint, ...item } : item))
    : [...current, checkpoint];
  return next.sort((left, right) => Date.parse(left.created_at) - Date.parse(right.created_at));
}

function mergeFetchedCheckpoints(
  current: CheckpointRecord[],
  fetched: CheckpointRecord[]
): CheckpointRecord[] {
  const checkpointsByID = new Map(current.map((checkpoint) => [checkpoint.checkpoint_id, checkpoint]));
  for (const checkpoint of fetched) {
    const existing = checkpointsByID.get(checkpoint.checkpoint_id);
    checkpointsByID.set(checkpoint.checkpoint_id, existing ? { ...existing, ...checkpoint } : checkpoint);
  }
  return [...checkpointsByID.values()].sort(
    (left, right) => Date.parse(left.created_at) - Date.parse(right.created_at)
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function stringField(record: Record<string, unknown>, field: string): string {
  const value = record[field];
  return typeof value === "string" ? value.trim() : "";
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

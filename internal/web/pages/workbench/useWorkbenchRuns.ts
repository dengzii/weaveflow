import { startTransition, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  cancelRun,
  compareRuns,
  deleteRun,
  forkRun,
  getRunInspection,
  listEvents,
  listRuns,
  pauseRun,
  resumeRun,
  startRun,
} from "../../api";
import { emitRuntimeEvents } from "../../lib/runtimeEvents";
import { parseJSON } from "../../lib/utils";
import type {
  CheckpointRecord,
  GraphDefinition,
  RunInterrupt,
  RunInspection,
  RunComparison,
  RunRecord,
  RuntimeEvent,
  StepRecord,
  TriggerType,
} from "../../types";
import {
  useRuntimeEventStream,
  type RuntimeEventStreamDiagnostics,
  type RuntimeEventStreamGap,
  type RuntimeEventStreamStatus,
} from "./useRuntimeEventStream";
import {
  pendingUserInputState,
  userInputPromptFromInterrupt,
  type UserInputPrompt,
} from "./userInputModel";
import {
  checkpointRecordFromEvent,
  mergeFetchedCheckpoints,
  rememberRunInspection,
  upsertCheckpoint,
} from "./runInspectionModel";
import {
  canResumeRun,
  isActiveRunStatus,
  markRunResuming,
  matchesGraphIdentity,
  mergeLiveRuntimeEvents,
  mergeRefreshedRuns,
  mergeStoredRuntimeEvents,
  partitionLaunchRuntimeEvents,
  reconcileRunEvents,
  runListEventActionForKnownRun,
  runControlModeFromRun,
  runStatusFromEvent,
  runTriggerTypesFromRuns,
  selectedRunIDAfterDeletion,
  shouldProjectRuntimeEventToRun,
  upsertInspectedRun,
  upsertRunFromEvent,
  type GraphIdentity,
  type RunControlMode,
} from "./workbenchRunModel";

const RUN_STATUS_VISIBLE_STORAGE_KEY = "weaveflow.workbench.runStatus.visible";
const LIVE_EVENT_FLUSH_INTERVAL_MS = 80;

export function isCurrentRunLaunch(
  runContextVersion: number,
  launchGeneration: number,
  currentRunContextVersion: number,
  currentLaunchGeneration: number
): boolean {
  return runContextVersion === currentRunContextVersion && launchGeneration === currentLaunchGeneration;
}

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
  runInspectionLoading: boolean;
  steps: StepRecord[];
  checkpoints: CheckpointRecord[];
  runComparison: RunComparison | null;
  runComparisonLoading: boolean;
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
  streamStatus: RuntimeEventStreamStatus;
  streamDiagnostics: RuntimeEventStreamDiagnostics & {
    selectedEventsPerSecond: number;
    unselectedEventsPerSecond: number;
    selectedEventRatio: number;
    handlingDurationMS: number;
  };
  reconnectEventStream: () => void;
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
  forkSelectedRun: () => Promise<void>;
  compareSelectedRun: (otherRunID: string) => Promise<void>;
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
  const [runComparison, setRunComparison] = useState<RunComparison | null>(null);
  const [runComparisonLoading, setRunComparisonLoading] = useState(false);
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
  const [eventHandlingMetrics, setEventHandlingMetrics] = useState({
    selectedEventsPerSecond: 0,
    unselectedEventsPerSecond: 0,
    selectedEventRatio: 0,
    handlingDurationMS: 0,
  });
  const runsRef = useRef<RunRecord[]>([]);
  const runsByIDRef = useRef<Map<string, RunRecord>>(new Map());
  const runsRefreshStartedRef = useRef(0);
  const runBusyRef = useRef(false);
  const selectedRunIDRef = useRef("");
  const userRunSelectionVersionRef = useRef(0);
  const launchPendingRef = useRef(false);
  const launchRunIDRef = useRef("");
  const launchGenerationRef = useRef(0);
  const launchBufferedEventsRef = useRef<RuntimeEvent[]>([]);
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
  const processedEventIDsRef = useRef<Set<string>>(new Set());
  const processedEventIDOrderRef = useRef<string[]>([]);
  const pendingEventHandlingMetricsRef = useRef({
    selectedEvents: 0,
    unselectedEvents: 0,
    handlingDurationMS: 0,
  });
  const eventHandlingMetricsTimerRef = useRef<number | null>(null);

  const discardPendingLiveEvents = useCallback(() => {
    pendingLiveEventsRef.current = [];
    if (liveEventsFlushTimerRef.current !== null) {
      window.clearTimeout(liveEventsFlushTimerRef.current);
      liveEventsFlushTimerRef.current = null;
    }
  }, []);

  const flushEventHandlingMetrics = useCallback(() => {
    eventHandlingMetricsTimerRef.current = null;
    const pending = pendingEventHandlingMetricsRef.current;
    pendingEventHandlingMetricsRef.current = {
      selectedEvents: 0,
      unselectedEvents: 0,
      handlingDurationMS: 0,
    };
    const totalEvents = pending.selectedEvents + pending.unselectedEvents;
    setEventHandlingMetrics({
      selectedEventsPerSecond: pending.selectedEvents,
      unselectedEventsPerSecond: pending.unselectedEvents,
      selectedEventRatio: totalEvents > 0 ? pending.selectedEvents / totalEvents : 0,
      handlingDurationMS: pending.handlingDurationMS,
    });
  }, []);

  const recordEventHandling = useCallback((selected: boolean, durationMS: number) => {
    if (selected) {
      pendingEventHandlingMetricsRef.current.selectedEvents += 1;
    } else {
      pendingEventHandlingMetricsRef.current.unselectedEvents += 1;
    }
    pendingEventHandlingMetricsRef.current.handlingDurationMS += durationMS;
    if (eventHandlingMetricsTimerRef.current !== null) return;
    eventHandlingMetricsTimerRef.current = window.setTimeout(flushEventHandlingMetrics, 1_000);
  }, [flushEventHandlingMetrics]);

  const clearEventHandlingMetrics = useCallback(() => {
    if (eventHandlingMetricsTimerRef.current !== null) {
      window.clearTimeout(eventHandlingMetricsTimerRef.current);
      eventHandlingMetricsTimerRef.current = null;
    }
    pendingEventHandlingMetricsRef.current = {
      selectedEvents: 0,
      unselectedEvents: 0,
      handlingDurationMS: 0,
    };
    setEventHandlingMetrics({
      selectedEventsPerSecond: 0,
      unselectedEventsPerSecond: 0,
      selectedEventRatio: 0,
      handlingDurationMS: 0,
    });
  }, []);

  const clearLiveEvents = useCallback(() => {
    liveEventsVersionRef.current += 1;
    discardPendingLiveEvents();
    processedEventIDsRef.current.clear();
    processedEventIDOrderRef.current = [];
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
    emitRuntimeEvents(incoming);
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
    runsByIDRef.current = new Map(next.map((run) => [run.run_id, run]));
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
    replaceStoredEventPage(inspection.events.items, inspection.events.next_cursor);
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
    setRunComparison(null);
    setRunComparisonLoading(false);
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

  useEffect(() => () => {
    discardPendingLiveEvents();
    if (eventHandlingMetricsTimerRef.current !== null) {
      window.clearTimeout(eventHandlingMetricsTimerRef.current);
    }
  }, [discardPendingLiveEvents]);

  const resetRunState = useCallback(() => {
    runContextVersionRef.current += 1;
    launchGenerationRef.current += 1;
    runBusyRef.current = false;
    launchPendingRef.current = false;
    launchRunIDRef.current = "";
    launchBufferedEventsRef.current = [];
    ignoredHumanInterruptsRef.current.clear();
    runInspectionCacheRef.current.clear();
    setRunLaunchPending(false);
    setRunBusy(false);
    setRunInspectionLoading(false);
    updateRuns([]);
    updateSelectedRunID("");
    clearLiveEvents();
    clearEventHandlingMetrics();
    setRunTriggerTypes({});
    clearSelectedRunInspection();
  }, [clearEventHandlingMetrics, clearLiveEvents, clearSelectedRunInspection, updateRuns, updateSelectedRunID]);

  const syncUserInputPrompt = useCallback((interrupt: RunInterrupt | null, run: RunRecord | null) => {
    const prompt = userInputPromptFromInterrupt(interrupt, definition, run);
    if (!prompt) {
      humanPromptCheckpointRef.current = "";
      setHumanPrompt(null);
      setHumanPromptText("");
      return;
    }
    if (ignoredHumanInterruptsRef.current.has(prompt.checkpointID)) return;
    if (humanPromptCheckpointRef.current === prompt.checkpointID) return;
    humanPromptCheckpointRef.current = prompt.checkpointID;
    setHumanPrompt(prompt);
    setHumanPromptText("");
  }, [definition]);

  // The pause event can arrive before detail state, so prompt discovery follows both run and interrupt updates.
  useEffect(() => {
    syncUserInputPrompt(runInterrupt, selectedRun);
  }, [runInterrupt, selectedRun, syncUserInputPrompt]);

  const refreshRuns = useCallback(async (
    identity: GraphIdentity = graphIdentityRef.current,
    autoSelect = true
  ) => {
    const contextVersion = runContextVersionRef.current;
    const runsAtStart = runsRef.current;
    const refreshRequest = ++runsRefreshStartedRef.current;
    try {
      const nextRuns = await listRuns(identity.id);
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
      setRunTriggerTypes(runTriggerTypesFromRuns(mergedRuns, identity.id));
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
    const graphID = runsByIDRef.current.get(runID)?.graph_id || graphIdentityRef.current.id;
    let inspection: RunInspection;
    try {
      inspection = await getRunInspection(graphID, runID);
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
    runInspectionCacheRef.current = rememberRunInspection(runInspectionCacheRef.current, inspection);
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
    const graphID = runsByIDRef.current.get(runID)?.graph_id || graphIdentityRef.current.id;
    eventPageLoadingRef.current = true;
    setOlderEventsLoading(true);
    try {
      const page = await listEvents(graphID, runID, cursor);
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

  const trackRuntimeEvent = useCallback((event: RuntimeEvent) => {
    if (event.id && processedEventIDsRef.current.has(event.id)) return;
    if (event.id) {
      processedEventIDsRef.current.add(event.id);
      processedEventIDOrderRef.current.push(event.id);
      if (processedEventIDOrderRef.current.length > 10_000) {
        const expired = processedEventIDOrderRef.current.splice(0, 1_000);
        for (const eventID of expired) processedEventIDsRef.current.delete(eventID);
      }
    }

    enqueueLiveEvent(event);
    const checkpoint = checkpointRecordFromEvent(event);
    if (checkpoint) {
      setCheckpoints((current) => upsertCheckpoint(current, checkpoint));
    }
    if (event.run_id && event.type === "run.paused") {
      void refreshSelectedRun(event.run_id).catch(reportError);
    }
  }, [enqueueLiveEvent, refreshSelectedRun, reportError]);

  const handleRuntimeEvent = useCallback((event: RuntimeEvent) => {
    const startedAt = performance.now();
    const listAction = runListEventActionForKnownRun(runsByIDRef.current.has(event.run_id), event);
    const nextStatus = runStatusFromEvent(event.type);
    if (listAction === "update" && shouldProjectRuntimeEventToRun(event)) {
      updateRuns((current) => upsertRunFromEvent(current, event, nextStatus, graphIdentityRef.current));
    } else if (listAction === "refresh") {
      requestEventRunRefresh();
    }

    const selectedRun = selectedRunIDRef.current;
    if (launchPendingRef.current && !launchRunIDRef.current) {
      launchBufferedEventsRef.current.push(event);
      if (launchBufferedEventsRef.current.length > 5_000) {
        launchBufferedEventsRef.current.splice(0, launchBufferedEventsRef.current.length - 5_000);
      }
      recordEventHandling(false, performance.now() - startedAt);
      return;
    }
    if (!event.run_id || event.run_id !== selectedRun) {
      recordEventHandling(false, performance.now() - startedAt);
      return;
    }

    trackRuntimeEvent(event);
    recordEventHandling(true, performance.now() - startedAt);
  }, [recordEventHandling, requestEventRunRefresh, trackRuntimeEvent, updateRuns]);

  const handleRuntimeEventGap = useCallback((gap: RuntimeEventStreamGap) => {
    const contextVersion = runContextVersionRef.current;
    void (async () => {
      try {
        await refreshRuns(graphIdentityRef.current, false);
        if (runContextVersionRef.current !== contextVersion) return;
        const runID = selectedRunIDRef.current;
        if (runID) await refreshSelectedRun(runID);
        if (runContextVersionRef.current !== contextVersion) return;
        onNotify(
          "warn",
          `Runtime event gap (${gap.reason}); persistent run data was reconciled. Live LLM chunks may be incomplete.`
        );
      } catch (error) {
        if (runContextVersionRef.current === contextVersion) reportError(error);
      }
    })();
  }, [onNotify, refreshRuns, refreshSelectedRun, reportError]);

  const stream = useRuntimeEventStream(graphIdentity.id, handleRuntimeEvent, handleRuntimeEventGap);
  const streamStatus = stream.status;

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
    setRunComparison(null);
  }, [applyRunInspection, updateSelectedRunID]);

  const startConfiguredRun = useCallback(async (
    initialState: unknown,
    identity: GraphIdentity = graphIdentityRef.current
  ) => {
    if (launchPendingRef.current || runBusyRef.current) return;
    const userSelectionVersion = userRunSelectionVersionRef.current;
    graphIdentityRef.current = identity;
    const runContextVersion = ++runContextVersionRef.current;
    const launchGeneration = ++launchGenerationRef.current;
    launchPendingRef.current = true;
    launchRunIDRef.current = "";
    launchBufferedEventsRef.current = [];
    setRunLaunchPending(true);
    updateSelectedRunID("");
    clearLiveEvents();
    clearEventHandlingMetrics();
    ignoredHumanInterruptsRef.current.clear();
    clearSelectedRunInspection();
    setRunStatusVisible(true);
    try {
      if (!identity.sessionID) throw new Error(`graph ${identity.id} has no configured session`);
      const run = await startRun(identity.id, identity.sessionID, initialState);
      if (!isCurrentRunLaunch(
        runContextVersion,
        launchGeneration,
        runContextVersionRef.current,
        launchGenerationRef.current
      )) {
        return;
      }
      launchRunIDRef.current = run.run_id;
      launchPendingRef.current = false;
      setRunLaunchPending(false);
      if (userRunSelectionVersionRef.current === userSelectionVersion) {
        updateSelectedRunID(run.run_id);
        setRunInterrupt(null);
      }
      const bufferedEvents = partitionLaunchRuntimeEvents(
        launchBufferedEventsRef.current,
        run.run_id
      );
      launchBufferedEventsRef.current = [];
      if (selectedRunIDRef.current === run.run_id) {
        for (const event of bufferedEvents.matched) trackRuntimeEvent(event);
      }
      launchRunIDRef.current = "";
      await refreshRuns(identity);
      if (!isCurrentRunLaunch(
        runContextVersion,
        launchGeneration,
        runContextVersionRef.current,
        launchGenerationRef.current
      )) {
        return;
      }
      if (isRunSelectionCurrent(run.run_id)) {
        await refreshSelectedRun(run.run_id);
      }
      if (!isCurrentRunLaunch(
        runContextVersion,
        launchGeneration,
        runContextVersionRef.current,
        launchGenerationRef.current
      )) {
        return;
      }
      onNotify("info", `Run ${run.status}: ${run.run_id}`);
    } catch (error) {
      if (!isCurrentRunLaunch(
        runContextVersion,
        launchGeneration,
        runContextVersionRef.current,
        launchGenerationRef.current
      )) {
        return;
      }
      launchPendingRef.current = false;
      launchRunIDRef.current = "";
      launchBufferedEventsRef.current = [];
      setRunLaunchPending(false);
      reportError(error);
    }
  }, [clearEventHandlingMetrics, clearLiveEvents, clearSelectedRunInspection, isRunSelectionCurrent, onNotify, refreshRuns, refreshSelectedRun, reportError, trackRuntimeEvent, updateSelectedRunID]);

  const controlSelectedRun = useCallback(async (kind: "pause" | "cancel") => {
    const runID = selectedRunIDRef.current;
    if (!runID || !beginRunOperation({ allowDuringLaunch: true })) return;
    const runContextVersion = runContextVersionRef.current;
    const selected = runsRef.current.find((run) => run.run_id === runID) ?? null;
    try {
      const run = await (kind === "pause"
        ? pauseRun(selected?.graph_id || graphIdentityRef.current.id, runID)
        : cancelRun(selected?.graph_id || graphIdentityRef.current.id, runID));
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
      await deleteRun(target.graph_id || graphIdentityRef.current.id, runID);
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

  const forkSelectedRun = useCallback(async () => {
    const sourceRunID = selectedRunIDRef.current;
    const source = runsRef.current.find((run) => run.run_id === sourceRunID) ?? null;
    const checkpointID = source?.last_checkpoint_id || "";
    if (!source || !checkpointID || !beginRunOperation()) return;
    const runContextVersion = runContextVersionRef.current;
    try {
      const identity = { id: source.graph_id || graphIdentityRef.current.id, version: source.graph_version || graphIdentityRef.current.version };
      const requestKey = `workbench-${sourceRunID}-${checkpointID}-${Date.now()}`;
      const result = await forkRun(identity.id, sourceRunID, checkpointID, requestKey);
      if (runContextVersionRef.current !== runContextVersion) return;
      updateRuns((current) => upsertInspectedRun(current, result.run));
      await refreshRuns(identity, false);
      if (runContextVersionRef.current !== runContextVersion) return;
      updateSelectedRunID(result.run.run_id);
      await refreshSelectedRun(result.run.run_id);
      onNotify("info", `Fork created: ${result.run.run_id}`);
    } catch (error) {
      if (runContextVersionRef.current === runContextVersion) reportError(error);
    } finally {
      finishRunOperation(runContextVersion);
    }
  }, [beginRunOperation, finishRunOperation, onNotify, refreshRuns, refreshSelectedRun, reportError, updateRuns, updateSelectedRunID]);

  const compareSelectedRun = useCallback(async (otherRunID: string) => {
    const leftRunID = selectedRunIDRef.current;
    if (!leftRunID || !otherRunID || leftRunID === otherRunID || !beginRunOperation()) return;
    const left = runsRef.current.find((run) => run.run_id === leftRunID) ?? null;
    const runContextVersion = runContextVersionRef.current;
    setRunComparisonLoading(true);
    try {
      const graphID = left?.graph_id || graphIdentityRef.current.id;
      const comparison = await compareRuns(graphID, leftRunID, otherRunID);
      if (runContextVersionRef.current !== runContextVersion) return;
      setRunComparison(comparison);
      onNotify("info", `Compared ${leftRunID} with ${otherRunID}`);
    } catch (error) {
      if (runContextVersionRef.current === runContextVersion) reportError(error);
    } finally {
      if (runContextVersionRef.current === runContextVersion) setRunComparisonLoading(false);
      finishRunOperation(runContextVersion);
    }
  }, [beginRunOperation, finishRunOperation, onNotify, reportError]);

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
    const selected = runsRef.current.find((run) => run.run_id === runID) ?? null;
    const prompt = userInputPromptFromInterrupt(runInterrupt, definition, selected);
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
    const identity = {
      id: selected?.graph_id || graphIdentityRef.current.id,
      version: selected?.graph_version || graphIdentityRef.current.version,
    };
    try {
      const input = parseJSON<unknown>(initialStateText);
      updateRuns((current) => markRunResuming(current, runID));
      setRunInterrupt(null);
      const result = await resumeRun(identity.id, runID, input);
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
        identity.id,
        prompt.runID,
        pendingUserInputState(prompt.statePath, text)
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
    runComparison,
    runComparisonLoading,
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
    streamDiagnostics: {
      ...stream.diagnostics,
      ...eventHandlingMetrics,
    },
    reconnectEventStream: stream.reconnect,
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

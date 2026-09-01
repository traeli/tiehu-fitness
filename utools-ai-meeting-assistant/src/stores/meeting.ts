import { computed, ref } from "vue";
import { defineStore } from "pinia";

import {
  getMeetingGateway,
  getRecordingRepository,
  requiresBrowserAuthentication,
} from "@/application/runtime";
import {
  MeetingCompletionError,
  waitForMeetingCompletion,
} from "@/application/meetingCompletion";
import {
  toMeetingError,
  type MeetingErrorInfo,
} from "@/application/meetingError";
import { renderSummaryMarkdown } from "@/application/summaryMarkdown";
import { appConfig } from "@/config";
import {
  canStartMeeting,
  canStopMeeting,
  requiresMeetingCleanup,
  type ClientMeetingPhase,
  type MeetingSession,
  type MeetingQuota,
  type MeetingStatus,
  type MeetingSummary,
  type MeetingSummaryStatus,
  type TranscriptSegment,
} from "@/domain/meeting";
import {
  BrowserMeetingAudioRecorder,
  type AudioRecorder,
  type CapturedAudio,
} from "@/infrastructure/audio/microphoneRecorder";
import { SyntheticPCMRecorder } from "@/infrastructure/audio/syntheticRecorder";
import { getDesktopBridge } from "@/infrastructure/desktop/desktopBridge";
import {
  TranscriptionClient,
  type RealtimeConnectionState,
} from "@/infrastructure/realtime/transcriptionClient";
import type { LocalMeetingRecording } from "@/infrastructure/recording/recordingRepository";

export const useMeetingStore = defineStore("meeting", () => {
  const phase = ref<ClientMeetingPhase>("idle");
  const session = ref<MeetingSession>();
  const transcript = ref<TranscriptSegment[]>([]);
  const summary = ref<MeetingSummary>();
  const elapsedSeconds = ref(0);
  const meetingError = ref<MeetingErrorInfo>();
  const quota = ref<MeetingQuota>();
  const quotaLoading = ref(false);
  const quotaError = ref<string>();
  const retainAudio = ref(false);
  const captureSystemAudio = ref(window.meetingDesktop !== undefined);
  const captureMicrophone = ref(true);
  const systemAudioLevel = ref(0);
  const mixedAudioLevel = ref(0);
  const systemAudioSignalSeen = ref(false);
  const transcriptionConsent = ref(false);
  const connectionState = ref<RealtimeConnectionState>("idle");
  const runtimeInfo = ref(getDesktopBridge().getRuntimeInfo());
  const pluginVisibility = ref<"visible" | "hidden">("visible");
  const recordings = ref<LocalMeetingRecording[]>([]);
  const recordingsLoading = ref(false);
  const recordingDirectory = ref(getDesktopBridge().getRecordingDirectory());
  const selectedRecordingID = ref<string>();
  const recordingPlaybackURL = ref<string>();
  const recordingTranscript = ref<TranscriptSegment[]>([]);
  const recordingMeetingStatus = ref<MeetingStatus>();
  const recordingSummary = ref<MeetingSummary>();
  const recordingSummaryStatus = ref<MeetingSummaryStatus>("not_started");
  const recordingSummaryFailure = ref<string>();
  const recordingSummaryRegenerating = ref(false);
  const recordingDetailLoading = ref(false);
  const recordingDetailError = ref<string>();

  let recorder: AudioRecorder | undefined;
  let transcriptionClient: TranscriptionClient | undefined;
  let elapsedTimer: number | undefined;
  let mockTimers: number[] = [];
  let unsubscribeLifecycle: (() => void) | undefined;
  let recordingSelectionVersion = 0;
  let recordingSummaryPollTimer: number | undefined;
  let quotaRefreshVersion = 0;
  let startOperationVersion = 0;
  let stopIdempotencyKey: string | undefined;
  let lifecycleCleanupPromise: Promise<void> | undefined;
  let pageHideListenerInstalled = false;

  const canStart = computed(() => canStartMeeting(phase.value));
  const canStop = computed(() => canStopMeeting(phase.value));
  const statusText = computed(() => {
    if (phase.value === "starting" && connectionState.value === "connecting") {
      return "正在连接实时转写";
    }
    if (phase.value === "recording" && connectionState.value === "disconnected") {
      return "录制中 · 转写连接中断";
    }
    return statusLabels[phase.value];
  });
  const connectionStatusText = computed(() => connectionStatusLabels[connectionState.value]);
  const durationText = computed(() => formatDuration(elapsedSeconds.value));

  function initializeRuntime(): void {
    runtimeInfo.value = getDesktopBridge().getRuntimeInfo();
    recordingDirectory.value = getDesktopBridge().getRecordingDirectory();
    unsubscribeLifecycle?.();
    unsubscribeLifecycle = getDesktopBridge().subscribeLifecycle(handleLifecycleEvent);
    if (!pageHideListenerInstalled) {
      window.addEventListener("pagehide", handlePageHide);
      pageHideListenerInstalled = true;
    }
    void refreshRecordings();
    if (!requiresBrowserAuthentication()) {
      void refreshQuota();
    }
  }

  function disposeRuntime(): void {
    unsubscribeLifecycle?.();
    unsubscribeLifecycle = undefined;
    if (pageHideListenerInstalled) {
      window.removeEventListener("pagehide", handlePageHide);
      pageHideListenerInstalled = false;
    }
    transcriptionClient?.close();
    void cleanupMeetingForLifecycle(false);
    clearRecordingSelection();
  }

  async function startMeeting(): Promise<void> {
    if (!canStart.value) {
      return;
    }
    if (!transcriptionConsent.value) {
      meetingError.value = {
        code: "UNKNOWN_ERROR",
        title: "需要确认云端转写",
        message: "请先确认音频会临时传输到云端进行语音识别。",
        retryable: false,
      };
      return;
    }
    resetMeetingView();
    phase.value = "starting";
    const operationVersion = ++startOperationVersion;

    let gateway: Awaited<ReturnType<typeof getMeetingGateway>> | undefined;
    try {
      gateway = await getMeetingGateway();
      assertStartOperationActive(operationVersion);
      const created = await gateway.createMeeting({
        language: "auto",
        retainAudio: retainAudio.value,
        transcriptionConsent: transcriptionConsent.value,
        idempotencyKey: crypto.randomUUID(),
      });
      session.value = created;
      stopIdempotencyKey = crypto.randomUUID();
      assertStartOperationActive(operationVersion);
      void refreshQuota();

      if (created.websocketUrl && created.sessionTicket) {
        transcriptionClient = new TranscriptionClient({
          url: created.websocketUrl,
          sessionTicket: created.sessionTicket,
          audio: created.audio,
          onSegment: upsertTranscriptSegment,
          onError: (error) => {
            meetingError.value = toMeetingError(error);
          },
          onConnectionStateChange: (state) => {
            connectionState.value = state;
          },
        });
        await transcriptionClient.connect();
        assertStartOperationActive(operationVersion);
      } else {
        connectionState.value = "connected";
      }

      recorder = appConfig.useSyntheticAudio
        ? new SyntheticPCMRecorder(created.audio)
        : new BrowserMeetingAudioRecorder(
            created.audio,
            {
              captureSystemAudio: captureSystemAudio.value,
              captureMicrophone: captureMicrophone.value,
            },
            getDesktopBridge(),
          );
      await recorder.start((chunk, capturedAt) => {
        transcriptionClient?.sendAudioChunk(chunk, capturedAt);
      }, (error) => {
        const captureFailure = toMeetingError(error, "stop");
        meetingError.value = captureFailure;
        window.setTimeout(() => {
          if (phase.value === "recording") {
            void performStopMeeting().finally(() => {
              meetingError.value = captureFailure;
            });
          }
        }, 0);
      }, (source, level) => {
        if (source === "system") {
          systemAudioLevel.value = level.peak;
          if (level.peak >= 0.0001) {
            systemAudioSignalSeen.value = true;
          }
          return;
        }
        mixedAudioLevel.value = level.peak;
      });
      assertStartOperationActive(operationVersion);

      phase.value = "recording";
      startElapsedTimer();
      if (appConfig.useMockApi) {
        scheduleMockTranscript();
      }
    } catch (error) {
      const interruptedByLifecycle = operationVersion !== startOperationVersion;
      try {
        await cleanupCapture(false);
      } catch (cleanupError) {
        console.error("cleanup audio capture after startup failure", cleanupError);
      }
      if (gateway && session.value) {
        try {
          await gateway.stopMeeting(session.value.meetingId, stopIdempotencyKey ?? crypto.randomUUID());
        } catch (compensationError) {
          console.error("stop meeting after audio capture startup failure", {
            meetingId: session.value.meetingId,
            error: compensationError,
          });
        }
      }
      phase.value = interruptedByLifecycle ? "cancelled" : "failed";
      meetingError.value = interruptedByLifecycle ? undefined : toMeetingError(error, "start");
      void refreshQuota();
    }
  }

  async function stopMeeting(): Promise<void> {
    return performStopMeeting();
  }

  async function performStopMeeting(): Promise<void> {
    if (!canStop.value || !session.value) {
      return;
    }
    meetingError.value = undefined;
    phase.value = "stopping";
    clearTimers();

    try {
      let captureError: unknown;
      try {
        await cleanupCapture(true);
      } catch (error) {
        captureError = error;
      }
      phase.value = "processing";
      const gateway = await getMeetingGateway();
      const stopped = await gateway.stopMeeting(
        session.value.meetingId,
        stopIdempotencyKey ?? (stopIdempotencyKey = crypto.randomUUID()),
      );
      phase.value = stopped.status;
      void refreshQuota();
      getDesktopBridge().notify("本地录音已保存，会议纪要将在后台生成");
      if (stopped.status === "processing") {
        void trackMeetingCompletion(gateway, stopped);
      }
      if (captureError !== undefined) {
        const mapped = toMeetingError(captureError);
        meetingError.value = {
          ...mapped,
          title: "本地录音保存失败",
          message: `${mapped.message} 会议停止流程已继续完成。`,
          retryable: false,
        };
      }
    } catch (error) {
      phase.value = "cancelled";
      void refreshQuota();
      const mapped = toMeetingError(error, "stop");
      meetingError.value = {
        ...mapped,
        title: "会议处理已取消",
        message:
          error instanceof MeetingCompletionError
            ? error.message
            : `${mapped.message} 已停止等待会议处理结果。`,
        retryable: false,
        failedAction: undefined,
      };
    }
  }

  async function trackMeetingCompletion(
    gateway: Awaited<ReturnType<typeof getMeetingGateway>>,
    initial: { meetingId: string; status: MeetingStatus },
  ): Promise<void> {
    try {
      const result = await waitForMeetingCompletion(gateway, initial, { timeoutMs: 180_000 });
      if (session.value?.meetingId !== initial.meetingId) {
        return;
      }
      phase.value = result.status;
      void refreshQuota();
      const summaryResult = await gateway.getMeetingSummary(initial.meetingId);
      summary.value = summaryResult.summary;
      if (summaryResult.status === "succeeded") {
        getDesktopBridge().notify("会议纪要已生成");
      } else if (summaryResult.status === "failed") {
        getDesktopBridge().notify("会议转写已保存，会议纪要生成失败");
      }
    } catch (error) {
      console.warn("background meeting completion polling stopped", {
        meetingId: initial.meetingId,
        error,
      });
      if (session.value?.meetingId !== initial.meetingId || phase.value !== "processing") {
        return;
      }
      phase.value = "cancelled";
      const mapped = toMeetingError(error);
      meetingError.value = {
        ...mapped,
        title: "会议处理已取消",
        message:
          error instanceof MeetingCompletionError
            ? error.message
            : `${mapped.message} 已停止等待会议处理结果。`,
        retryable: false,
        failedAction: undefined,
      };
      getDesktopBridge().notify("会议处理未完成，已停止生成纪要");
    }
  }

  async function exportMarkdown(): Promise<void> {
    if (!summary.value) {
      return;
    }
    try {
      const content = renderSummaryMarkdown(summary.value);
      const safeTopic = summary.value.topic.trim() || "meeting-summary";
      await getDesktopBridge().saveMarkdown({
        suggestedName: `${safeTopic}.md`,
        content,
      });
    } catch (error) {
      meetingError.value = toMeetingError(error, "export");
    }
  }

  async function deleteRecording(recordingID: string): Promise<void> {
    try {
      await getRecordingRepository().delete(recordingID);
      if (selectedRecordingID.value === recordingID) {
        clearRecordingSelection();
      }
      await refreshRecordings();
    } catch (error) {
      meetingError.value = {
        ...toMeetingError(error),
        title: "删除录音失败",
        retryable: false,
      };
    }
  }

  async function retryFailedAction(): Promise<void> {
    const failedAction = meetingError.value?.failedAction;
    if (!meetingError.value?.retryable || !failedAction) {
      return;
    }
    if (failedAction === "start") {
      meetingError.value = undefined;
      await startMeeting();
      return;
    }
    if (failedAction === "stop") {
      meetingError.value = undefined;
      return;
    }
    meetingError.value = undefined;
    await exportMarkdown();
  }

  function dismissError(): void {
    meetingError.value = undefined;
  }

  function upsertTranscriptSegment(segment: TranscriptSegment): void {
    const index = transcript.value.findIndex(
      (item) => item.id === segment.id || item.sequenceNo === segment.sequenceNo,
    );
    if (index >= 0) {
      transcript.value.splice(index, 1, segment);
    } else {
      transcript.value.push(segment);
      transcript.value.sort((left, right) => left.sequenceNo - right.sequenceNo);
    }
  }

  function resetMeetingView(): void {
    clearTimers();
    session.value = undefined;
    transcript.value = [];
    summary.value = undefined;
    elapsedSeconds.value = 0;
    meetingError.value = undefined;
    connectionState.value = "idle";
    systemAudioLevel.value = 0;
    mixedAudioLevel.value = 0;
    systemAudioSignalSeen.value = false;
    stopIdempotencyKey = undefined;
  }

  function assertStartOperationActive(operationVersion: number): void {
    if (operationVersion !== startOperationVersion) {
      throw new Error("会议启动已因插件退出而取消");
    }
  }

  function handlePageHide(): void {
    transcriptionClient?.close();
    void cleanupMeetingForLifecycle(false);
  }

  function cleanupMeetingForLifecycle(graceful: boolean): Promise<void> {
    if (lifecycleCleanupPromise) {
      return lifecycleCleanupPromise;
    }
    if (!requiresMeetingCleanup(phase.value)) {
      return Promise.resolve();
    }
    startOperationVersion += 1;
    clearTimers();
    const activeSession = session.value;
    const activeStopKey = stopIdempotencyKey ?? crypto.randomUUID();
    stopIdempotencyKey = activeStopKey;
    phase.value = "stopping";
    lifecycleCleanupPromise = (async () => {
      try {
        await cleanupCapture(graceful);
      } catch (error) {
        console.error("cleanup capture after plugin exit failed", error);
      }
      if (!activeSession) {
        phase.value = "cancelled";
        return;
      }
      try {
        const gateway = await getMeetingGateway();
        const stopped = await gateway.stopMeeting(activeSession.meetingId, activeStopKey);
        phase.value = stopped.status;
      } catch (error) {
        phase.value = "cancelled";
        console.error("stop meeting after plugin exit failed", {
          meetingId: activeSession.meetingId,
          error,
        });
      } finally {
        void refreshQuota();
      }
    })().finally(() => {
      lifecycleCleanupPromise = undefined;
    });
    return lifecycleCleanupPromise;
  }

  async function cleanupCapture(graceful: boolean): Promise<void> {
    const activeRecorder = recorder;
    const activeClient = transcriptionClient;
    recorder = undefined;
    transcriptionClient = undefined;
    let cleanupError: unknown;
    let capturedAudio: CapturedAudio | undefined;
    try {
      capturedAudio = await activeRecorder?.stop();
    } catch (error) {
      cleanupError = error;
    }
    if (graceful && capturedAudio && session.value) {
      try {
        await saveRecording(session.value.meetingId, capturedAudio);
      } catch (error) {
        cleanupError ??= error;
      }
    }
    try {
      if (graceful) {
        await activeClient?.finish();
      } else {
        activeClient?.close();
      }
    } catch (error) {
      cleanupError ??= error;
    } finally {
      if (!graceful || cleanupError !== undefined) {
        activeClient?.close();
      }
      connectionState.value = "closed";
    }
    if (cleanupError !== undefined) {
      throw cleanupError;
    }
  }

  async function saveRecording(meetingID: string, capturedAudio: CapturedAudio): Promise<void> {
    await getRecordingRepository().save({
      id: crypto.randomUUID(),
      meetingId: meetingID,
      createdAt: new Date().toISOString(),
      durationMs: Math.round(capturedAudio.durationMs),
      mimeType: capturedAudio.mimeType,
      audio: capturedAudio.blob,
    });
    await refreshRecordings();
  }

  async function refreshRecordings(): Promise<void> {
    recordingsLoading.value = true;
    try {
      const stored = await getRecordingRepository().list();
      recordings.value = stored;
      if (
        selectedRecordingID.value &&
        !stored.some((recording) => recording.id === selectedRecordingID.value)
      ) {
        clearRecordingSelection();
      }
    } catch (error) {
      meetingError.value = {
        ...toMeetingError(error),
        title: "读取录音失败",
        retryable: false,
      };
    } finally {
      recordingsLoading.value = false;
    }
  }

  async function refreshQuota(): Promise<void> {
    const refreshVersion = ++quotaRefreshVersion;
    quotaLoading.value = true;
    quotaError.value = undefined;
    try {
      const gateway = await getMeetingGateway();
      const current = await gateway.getMeetingQuota();
      if (refreshVersion === quotaRefreshVersion) {
        quota.value = current;
      }
    } catch (error) {
      if (refreshVersion === quotaRefreshVersion) {
        quotaError.value = describeError(error);
      }
    } finally {
      if (refreshVersion === quotaRefreshVersion) {
        quotaLoading.value = false;
      }
    }
  }

  async function selectRecording(recordingID: string): Promise<void> {
    if (!recordings.value.some((recording) => recording.id === recordingID)) {
      return;
    }
    const selectionVersion = ++recordingSelectionVersion;
    revokeRecordingPlaybackURL();
    selectedRecordingID.value = recordingID;
    recordingTranscript.value = [];
    recordingMeetingStatus.value = undefined;
    recordingSummary.value = undefined;
    recordingSummaryStatus.value = "not_started";
    recordingSummaryFailure.value = undefined;
    recordingDetailError.value = undefined;
    recordingDetailLoading.value = true;

    const recording = recordings.value.find((item) => item.id === recordingID);
    if (!recording) {
      recordingDetailLoading.value = false;
      return;
    }
    const audioResultPromise = getRecordingRepository().loadAudio(recordingID);
    const meetingGatewayPromise = getMeetingGateway();
    const meetingResultPromise = (async () => {
      const gateway = await meetingGatewayPromise;
      return Promise.all([
        gateway.getMeeting(recording.meetingId),
        gateway.listTranscriptSegments(recording.meetingId),
      ]);
    })();
    const summaryResultPromise = (async () => {
      const gateway = await meetingGatewayPromise;
      return gateway.getMeetingSummary(recording.meetingId);
    })();
    const [audioResult, meetingResult, summaryResult] = await Promise.allSettled([
      audioResultPromise,
      meetingResultPromise,
      summaryResultPromise,
    ]);
    if (selectionVersion !== recordingSelectionVersion) {
      return;
    }

    const errors: string[] = [];
    if (audioResult.status === "fulfilled") {
      recordingPlaybackURL.value = URL.createObjectURL(audioResult.value);
    } else {
      errors.push(`读取录音失败：${describeError(audioResult.reason)}`);
    }
    if (meetingResult.status === "fulfilled") {
      const [meeting, segments] = meetingResult.value;
      recordingMeetingStatus.value = meeting.status;
      recordingTranscript.value = segments;
    } else {
      errors.push(`读取会议内容失败：${describeError(meetingResult.reason)}`);
    }
    if (summaryResult.status === "fulfilled") {
      recordingSummaryStatus.value = summaryResult.value.status;
      recordingSummary.value = summaryResult.value.summary;
      recordingSummaryFailure.value = summaryResult.value.failureReason;
      if (summaryResult.value.status === "pending" || summaryResult.value.status === "processing") {
        scheduleRecordingSummaryPoll(recording.meetingId, selectionVersion);
      }
    } else {
      errors.push(`读取会议总结失败：${describeError(summaryResult.reason)}`);
    }
    recordingDetailError.value = errors.length > 0 ? errors.join("；") : undefined;
    recordingDetailLoading.value = false;
  }

  async function regenerateRecordingSummary(): Promise<void> {
    const recording = recordings.value.find((item) => item.id === selectedRecordingID.value);
    if (!recording || recordingSummaryRegenerating.value) {
      return;
    }
    recordingSummaryRegenerating.value = true;
    recordingSummaryFailure.value = undefined;
    try {
      const gateway = await getMeetingGateway();
      const result = await gateway.regenerateMeetingSummary(recording.meetingId, crypto.randomUUID());
      recordingSummaryStatus.value = result.status;
      scheduleRecordingSummaryPoll(recording.meetingId, recordingSelectionVersion);
    } catch (error) {
      recordingSummaryFailure.value = describeError(error);
      recordingSummaryStatus.value = "failed";
    } finally {
      recordingSummaryRegenerating.value = false;
    }
  }

  function scheduleRecordingSummaryPoll(meetingID: string, selectionVersion: number): void {
    clearRecordingSummaryPoll();
    recordingSummaryPollTimer = window.setTimeout(async () => {
      if (selectionVersion !== recordingSelectionVersion) {
        return;
      }
      try {
        const gateway = await getMeetingGateway();
        const result = await gateway.getMeetingSummary(meetingID);
        if (selectionVersion !== recordingSelectionVersion) {
          return;
        }
        recordingSummaryStatus.value = result.status;
        recordingSummary.value = result.summary;
        recordingSummaryFailure.value = result.failureReason;
        if (result.status === "pending" || result.status === "processing") {
          scheduleRecordingSummaryPoll(meetingID, selectionVersion);
        }
      } catch (error) {
        if (selectionVersion === recordingSelectionVersion) {
          recordingSummaryFailure.value = `刷新会议总结失败：${describeError(error)}`;
        }
      }
    }, 2_000);
  }

  function clearRecordingSummaryPoll(): void {
    if (recordingSummaryPollTimer !== undefined) {
      window.clearTimeout(recordingSummaryPollTimer);
      recordingSummaryPollTimer = undefined;
    }
  }

  function clearRecordingSelection(): void {
    recordingSelectionVersion += 1;
    clearRecordingSummaryPoll();
    revokeRecordingPlaybackURL();
    selectedRecordingID.value = undefined;
    recordingTranscript.value = [];
    recordingMeetingStatus.value = undefined;
    recordingSummary.value = undefined;
    recordingSummaryStatus.value = "not_started";
    recordingSummaryFailure.value = undefined;
    recordingSummaryRegenerating.value = false;
    recordingDetailError.value = undefined;
    recordingDetailLoading.value = false;
  }

  function revokeRecordingPlaybackURL(): void {
    if (recordingPlaybackURL.value) {
      URL.revokeObjectURL(recordingPlaybackURL.value);
      recordingPlaybackURL.value = undefined;
    }
  }

  function startElapsedTimer(): void {
    elapsedTimer = window.setInterval(() => {
      elapsedSeconds.value += 1;
    }, 1_000);
  }

  function scheduleMockTranscript(): void {
    const samples = [
      { delay: 900, speaker: "speaker_1", content: "我们先确认 AI 会议助手的 MVP 范围。" },
      { delay: 2_100, speaker: "speaker_2", content: "首期完成实时转写和 Markdown 纪要导出。" },
      { delay: 3_400, speaker: "speaker_1", content: "屏幕录制放到下一个版本。" },
    ];
    mockTimers = samples.map(({ delay, speaker, content }, index) =>
      window.setTimeout(() => {
        upsertTranscriptSegment({
          id: `mock-${index + 1}`,
          sequenceNo: index + 1,
          startOffsetMs: delay,
          endOffsetMs: delay + 800,
          speakerLabel: speaker,
          content,
          isFinal: true,
        });
      }, delay),
    );
  }

  function clearTimers(): void {
    if (elapsedTimer !== undefined) {
      window.clearInterval(elapsedTimer);
      elapsedTimer = undefined;
    }
    mockTimers.forEach((timer) => window.clearTimeout(timer));
    mockTimers = [];
  }

  function handleLifecycleEvent(event: MeetingPluginLifecycleEvent): void {
    switch (event.type) {
      case "enter":
        pluginVisibility.value = "visible";
        runtimeInfo.value = getDesktopBridge().getRuntimeInfo();
        void refreshQuota();
        return;
      case "detach":
        runtimeInfo.value = { ...runtimeInfo.value, windowType: "detach" };
        return;
      case "out":
        pluginVisibility.value = "hidden";
        void cleanupMeetingForLifecycle(true);
        return;
    }
  }

  return {
    phase,
    transcript,
    summary,
    meetingError,
    quota,
    quotaLoading,
    quotaError,
    retainAudio,
    captureSystemAudio,
    captureMicrophone,
    systemAudioLevel,
    mixedAudioLevel,
    systemAudioSignalSeen,
    transcriptionConsent,
    connectionState,
    connectionStatusText,
    runtimeInfo,
    pluginVisibility,
    recordings,
    recordingsLoading,
    recordingDirectory,
    selectedRecordingID,
    recordingPlaybackURL,
    recordingTranscript,
    recordingMeetingStatus,
    recordingSummary,
    recordingSummaryStatus,
    recordingSummaryFailure,
    recordingSummaryRegenerating,
    recordingDetailLoading,
    recordingDetailError,
    canStart,
    canStop,
    statusText,
    durationText,
    initializeRuntime,
    disposeRuntime,
    startMeeting,
    stopMeeting,
    exportMarkdown,
    selectRecording,
    regenerateRecordingSummary,
    deleteRecording,
    retryFailedAction,
    dismissError,
    refreshQuota,
  };
});

const statusLabels: Record<ClientMeetingPhase, string> = {
  idle: "未开始",
  starting: "正在启动",
  recording: "录制中",
  stopping: "正在停止",
  processing: "正在完成转写",
  completed: "已完成",
  partially_completed: "部分完成",
  failed: "处理失败",
  cancelled: "已取消",
};

const connectionStatusLabels: Record<RealtimeConnectionState, string> = {
  idle: "尚未连接",
  connecting: "正在连接",
  connected: "实时转写已连接",
  disconnected: "实时转写已断开",
  closed: "连接已关闭",
};

function formatDuration(seconds: number): string {
  const hours = Math.floor(seconds / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  const remainingSeconds = seconds % 60;
  return [hours, minutes, remainingSeconds].map((value) => String(value).padStart(2, "0")).join(":");
}

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : "未知错误";
}

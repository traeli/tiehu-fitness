import { computed, ref, watch } from "vue";
import { defineStore } from "pinia";

import { getRecordingRepository } from "@/application/runtime";
import { modelDownloadPercent, resolveLocalEngineSetupState, type LocalModelOperationPhase } from "@/application/localEngine";
import { toMeetingError, type MeetingErrorInfo } from "@/application/meetingError";
import { canStartMeeting, canStopMeeting, type ClientMeetingPhase, type LocalMeetingSummary, type MeetingSession, type MeetingSummaryProvider, type TranscriptSegment } from "@/domain/meeting";
import { BrowserMeetingAudioRecorder, type AudioRecorder, type CapturedAudio } from "@/infrastructure/audio/microphoneRecorder";
import { getDesktopBridge } from "@/infrastructure/desktop/desktopBridge";
import type { LocalMeetingRecording } from "@/infrastructure/recording/recordingRepository";
import { LocalTranscriber } from "@/infrastructure/transcription/localTranscriber";
import { summarizeMeeting } from "@/infrastructure/summary/ollamaMeetingSummarizer";

const localAudio = {
  mimeType: "audio/pcm;rate=16000",
  sampleRate: 16_000,
  channels: 1,
  chunkDurationMs: 200,
} as const;

export const useMeetingStore = defineStore("meeting", () => {
  const phase = ref<ClientMeetingPhase>("idle");
  const session = ref<MeetingSession>();
  const transcript = ref<TranscriptSegment[]>([]);
  const elapsedSeconds = ref(0);
  const meetingError = ref<MeetingErrorInfo>();
  const startOperationPending = ref(false);
  const stopOperationPending = ref(false);
  const captureSystemAudio = ref(window.meetingDesktop !== undefined);
  const captureMicrophone = ref(true);
  const systemAudioLevel = ref(0);
  const mixedAudioLevel = ref(0);
  const systemAudioSignalSeen = ref(false);
  const runtimeInfo = ref(getDesktopBridge().getRuntimeInfo());
  const recordings = ref<LocalMeetingRecording[]>([]);
  const recordingsLoading = ref(false);
  const recordingDirectory = ref(getDesktopBridge().getRecordingDirectory());
  const selectedRecordingID = ref<string>();
  const recordingPlaybackURL = ref<string>();
  const recordingTranscript = ref<TranscriptSegment[]>([]);
  const recordingDetailLoading = ref(false);
  const recordingDetailError = ref<string>();
  const recordingSummary = ref<LocalMeetingSummary>();
  const summaryGenerating = ref(false);
  const summaryError = ref<string>();
  const ollamaModels = ref<string[]>([]);
  const ollamaChecking = ref(false);
  const summaryProvider = ref<MeetingSummaryProvider>(readSummaryProvider());
  const ollamaBaseURL = ref(window.localStorage.getItem("tiehu.meeting.ollama-url") || "http://127.0.0.1:11434");
  const ollamaModel = ref(window.localStorage.getItem("tiehu.meeting.ollama-model") || "deepseek-r1:7b");
  const openAIBaseURL = "https://api.openai.com/v1";
  const openAIModel = ref(window.localStorage.getItem("tiehu.meeting.openai-model") || "gpt-4o-mini");
  const openAIApiKey = ref("");
  const deepSeekBaseURL = "https://api.deepseek.com";
  const deepSeekModel = ref(window.localStorage.getItem("tiehu.meeting.deepseek-model") || "deepseek-flash");
  const deepSeekApiKey = ref("");
  const summaryCustomPrompt = ref(window.localStorage.getItem("tiehu.meeting.summary-prompt") || "");
  const engineStatus = ref<LocalTranscriptionStatus>();
  const selectedTranscriptionModelId = ref<LocalTranscriptionModelID>(readTranscriptionModelID());
  const engineLoading = ref(false);
  const modelDownloadReceivedBytes = ref(0);
  const modelDownloadTotalBytes = ref(0);
  const modelOperationPhase = ref<LocalModelOperationPhase>("idle");
  const modelDownloadFailed = ref(false);

  let recorder: AudioRecorder | undefined;
  let localTranscriber: LocalTranscriber | undefined;
  let elapsedTimer: number | undefined;
  let unsubscribeLifecycle: (() => void) | undefined;
  let pageHideListenerInstalled = false;

  watch(summaryProvider, (value) => window.localStorage.setItem("tiehu.meeting.summary-provider", value));
  watch(ollamaBaseURL, (value) => window.localStorage.setItem("tiehu.meeting.ollama-url", value));
  watch(ollamaModel, (value) => window.localStorage.setItem("tiehu.meeting.ollama-model", value));
  watch(openAIModel, (value) => window.localStorage.setItem("tiehu.meeting.openai-model", value));
  watch(deepSeekModel, (value) => window.localStorage.setItem("tiehu.meeting.deepseek-model", value));
  watch(summaryCustomPrompt, (value) => window.localStorage.setItem("tiehu.meeting.summary-prompt", value));
  watch(selectedTranscriptionModelId, (value) => window.localStorage.setItem("tiehu.meeting.transcription-model", value));

  const canStart = computed(() =>
    canStartMeeting(phase.value) &&
    !startOperationPending.value &&
    !stopOperationPending.value &&
    engineStatus.value?.binaryAvailable === true &&
    engineStatus.value.modelDownloaded,
  );
  const canStop = computed(() => phase.value === "recording" && canStopMeeting(phase.value));
  const statusText = computed(() => statusLabels[phase.value]);
  const durationText = computed(() => formatDuration(elapsedSeconds.value));
  const engineSetupState = computed(() => resolveLocalEngineSetupState({
    status: engineStatus.value,
    operationPhase: modelOperationPhase.value,
    failed: modelDownloadFailed.value,
  }));
  const modelDownloadProgress = computed(() => modelDownloadPercent(
    modelDownloadReceivedBytes.value,
    modelDownloadTotalBytes.value,
  ));

  function initializeRuntime(): void {
    runtimeInfo.value = getDesktopBridge().getRuntimeInfo();
    recordingDirectory.value = getDesktopBridge().getRecordingDirectory();
    unsubscribeLifecycle?.();
    unsubscribeLifecycle = getDesktopBridge().subscribeLifecycle(handleLifecycleEvent);
    if (!pageHideListenerInstalled) {
      window.addEventListener("pagehide", handlePageHide);
      pageHideListenerInstalled = true;
    }
    void Promise.all([refreshRecordings(), refreshEngineStatus(), refreshOllamaModels(false)]);
  }

  function disposeRuntime(): void {
    unsubscribeLifecycle?.();
    unsubscribeLifecycle = undefined;
    if (pageHideListenerInstalled) {
      window.removeEventListener("pagehide", handlePageHide);
      pageHideListenerInstalled = false;
    }
    clearElapsedTimer();
    localTranscriber?.close();
    localTranscriber = undefined;
    void recorder?.stop().catch((error: unknown) => console.error("stop recorder on dispose", error));
    recorder = undefined;
    clearRecordingSelection();
  }

  async function refreshEngineStatus(): Promise<void> {
    try {
      engineStatus.value = await getDesktopBridge().getLocalTranscriptionStatus({ modelId: selectedTranscriptionModelId.value });
      modelDownloadFailed.value = false;
    } catch (error) {
      meetingError.value = localError(error, "读取本地转写组件失败");
    }
  }

  async function downloadModel(): Promise<void> {
    if (engineLoading.value) return;
    engineLoading.value = true;
    modelOperationPhase.value = "downloading";
    modelDownloadReceivedBytes.value = 0;
    modelDownloadTotalBytes.value = engineStatus.value?.modelSizeBytes ?? 0;
    modelDownloadFailed.value = false;
    meetingError.value = undefined;
    try {
      engineStatus.value = await getDesktopBridge().downloadLocalTranscriptionModel({ modelId: selectedTranscriptionModelId.value }, (progress) => {
        modelOperationPhase.value = progress.phase;
        modelDownloadReceivedBytes.value = Math.max(0, progress.receivedBytes);
        modelDownloadTotalBytes.value = Math.max(0, progress.totalBytes);
      });
      await refreshEngineStatus();
      if (engineStatus.value?.binaryAvailable !== true || !engineStatus.value.modelDownloaded) {
        throw new Error("本地语音模型下载完成，但运行状态校验未通过");
      }
      getDesktopBridge().notify("本地语音模型已准备完成");
    } catch (error) {
      modelDownloadFailed.value = true;
      const mapped = localError(error, "本地模型下载失败");
      meetingError.value = {
        ...mapped,
        code: "LOCAL_MODEL_DOWNLOAD_FAILED",
        retryable: true,
      };
    } finally {
      modelOperationPhase.value = "idle";
      engineLoading.value = false;
    }
  }

  async function selectTranscriptionModel(modelId: LocalTranscriptionModelID): Promise<void> {
    if (engineLoading.value || ["starting", "recording", "stopping", "processing"].includes(phase.value)) return;
    const modelExists = engineStatus.value?.models.some((model) => model.id === modelId) ?? true;
    if (!modelExists) return;
    selectedTranscriptionModelId.value = modelId;
    modelDownloadFailed.value = false;
    meetingError.value = undefined;
    await refreshEngineStatus();
  }

  async function startMeeting(): Promise<void> {
    if (!canStart.value) return;
    if (!captureSystemAudio.value && !captureMicrophone.value) {
      meetingError.value = localError(new Error("请至少选择一个音频来源"), "无法开始录音");
      return;
    }
    resetMeetingView();
    startOperationPending.value = true;
    phase.value = "starting";
    const meetingID = crypto.randomUUID();
    session.value = {
      meetingId: meetingID,
      status: "recording",
      startedAt: new Date().toISOString(),
      audio: localAudio,
    };
    try {
      localTranscriber = new LocalTranscriber(getDesktopBridge(), selectedTranscriptionModelId.value, upsertTranscriptSegment);
      await localTranscriber.start();
      recorder = new BrowserMeetingAudioRecorder(localAudio, {
        captureSystemAudio: captureSystemAudio.value,
        captureMicrophone: captureMicrophone.value,
      }, getDesktopBridge());
      await recorder.start(
        (chunk) => localTranscriber?.push(chunk),
        (error) => {
          meetingError.value = localError(error, "音频采集已中断");
          void stopMeeting();
        },
        (source, level) => {
          if (source === "system") {
            systemAudioLevel.value = level.peak;
            if (level.peak >= 0.0001) systemAudioSignalSeen.value = true;
          } else {
            mixedAudioLevel.value = level.peak;
          }
        },
      );
      phase.value = "recording";
      elapsedTimer = window.setInterval(() => { elapsedSeconds.value += 1; }, 1_000);
    } catch (error) {
      localTranscriber?.close();
      localTranscriber = undefined;
      await recorder?.stop().catch(() => undefined);
      recorder = undefined;
      phase.value = "failed";
      meetingError.value = localError(error, "本地会议启动失败", true);
    } finally {
      startOperationPending.value = false;
    }
  }

  async function stopMeeting(): Promise<void> {
    if (!canStop.value || stopOperationPending.value) return;
    stopOperationPending.value = true;
    phase.value = "stopping";
    clearElapsedTimer();
    const activeRecorder = recorder;
    const activeTranscriber = localTranscriber;
    recorder = undefined;
    localTranscriber = undefined;
    try {
      const capturedAudio = await activeRecorder?.stop();
      phase.value = "processing";
      await activeTranscriber?.finish();
      if (capturedAudio && session.value) {
        await saveRecording(session.value.meetingId, capturedAudio);
      }
      phase.value = "completed";
      getDesktopBridge().notify("本地录音和转写已保存");
    } catch (error) {
      activeTranscriber?.close();
      phase.value = "failed";
      const mapped = localError(error, "停止或保存本地会议失败");
      meetingError.value = {
        ...mapped,
        code: "LOCAL_MEETING_FINALIZATION_FAILED",
        retryable: false,
      };
    } finally {
      stopOperationPending.value = false;
    }
  }

  async function saveRecording(meetingID: string, capturedAudio: CapturedAudio): Promise<void> {
    await getRecordingRepository().save({
      id: crypto.randomUUID(),
      meetingId: meetingID,
      createdAt: new Date().toISOString(),
      durationMs: Math.max(1, Math.round(capturedAudio.durationMs)),
      mimeType: capturedAudio.mimeType,
      audio: capturedAudio.blob,
      transcript: transcript.value.slice(),
    });
    await refreshRecordings();
  }

  async function refreshRecordings(): Promise<void> {
    recordingsLoading.value = true;
    try {
      recordings.value = await getRecordingRepository().list();
    } catch (error) {
      meetingError.value = localError(error, "读取本地会议失败");
    } finally {
      recordingsLoading.value = false;
    }
  }

  async function selectRecording(recordingID: string): Promise<void> {
    if (!recordings.value.some((recording) => recording.id === recordingID)) return;
    clearRecordingSelection();
    selectedRecordingID.value = recordingID;
    recordingDetailLoading.value = true;
    recordingDetailError.value = undefined;
    try {
      const [audio, storedTranscript, storedSummary] = await Promise.all([
        getRecordingRepository().loadAudio(recordingID),
        getRecordingRepository().loadTranscript(recordingID),
        getRecordingRepository().loadSummary(recordingID),
      ]);
      recordingPlaybackURL.value = URL.createObjectURL(audio);
      recordingTranscript.value = storedTranscript;
      recordingSummary.value = storedSummary;
    } catch (error) {
      recordingDetailError.value = error instanceof Error ? error.message : "读取本地会议失败";
    } finally {
      recordingDetailLoading.value = false;
    }
  }

  async function refreshOllamaModels(reportError = true): Promise<void> {
    if (ollamaChecking.value) return;
    ollamaChecking.value = true;
    if (reportError) summaryError.value = undefined;
    try {
      const models = await getDesktopBridge().listOllamaModels(ollamaBaseURL.value);
      ollamaModels.value = models;
      if (!models.includes(ollamaModel.value)) {
        ollamaModel.value = models.find((model) => model.toLowerCase().includes("deepseek")) ?? models[0] ?? ollamaModel.value;
      }
      if (reportError && models.length === 0) {
        summaryError.value = "Ollama 已连接，但尚未安装模型。请先运行 ollama pull deepseek-r1:7b。";
      }
    } catch (error) {
      ollamaModels.value = [];
      if (reportError) summaryError.value = error instanceof Error ? error.message : "无法连接本机 Ollama";
    } finally {
      ollamaChecking.value = false;
    }
  }

  async function generateRecordingSummary(): Promise<void> {
    const recordingID = selectedRecordingID.value;
    if (!recordingID || recordingTranscript.value.length === 0 || summaryGenerating.value) return;
    summaryGenerating.value = true;
    summaryError.value = undefined;
    try {
      if (summaryProvider.value === "openai" && (!openAIModel.value.trim() || !openAIApiKey.value.trim())) {
        throw new Error("请填写 OpenAI 模型和 API Key");
      }
      if (summaryProvider.value === "deepseek" && (!deepSeekModel.value.trim() || !deepSeekApiKey.value.trim())) {
        throw new Error("请填写 DeepSeek 模型和 API Key");
      }
      const bridge = getDesktopBridge();
      const provider = summaryProvider.value;
      const model = provider === "ollama"
        ? ollamaModel.value
        : provider === "openai"
          ? openAIModel.value.trim()
          : deepSeekModel.value.trim();
      const generated = await summarizeMeeting({
        chat: (request) => provider === "ollama"
          ? bridge.chatOllama({ ...request, baseURL: ollamaBaseURL.value })
          : bridge.chatOpenAICompatible({
            ...request,
            provider,
            baseURL: provider === "openai" ? openAIBaseURL : deepSeekBaseURL,
            apiKey: provider === "openai" ? openAIApiKey.value.trim() : deepSeekApiKey.value.trim(),
            model,
          }),
      }, {
        provider,
        model,
        transcript: recordingTranscript.value,
        customPrompt: summaryCustomPrompt.value,
      });
      if (selectedRecordingID.value !== recordingID) return;
      await getRecordingRepository().saveSummary(recordingID, generated);
      recordingSummary.value = generated;
      getDesktopBridge().notify("本地会议总结已生成");
    } catch (error) {
      summaryError.value = error instanceof Error ? error.message : "本地会议总结生成失败";
    } finally {
      summaryGenerating.value = false;
    }
  }

  async function deleteRecording(recordingID: string): Promise<void> {
    try {
      await getRecordingRepository().delete(recordingID);
      if (selectedRecordingID.value === recordingID) clearRecordingSelection();
      await refreshRecordings();
    } catch (error) {
      meetingError.value = localError(error, "删除本地会议失败");
    }
  }

  async function exportTranscript(): Promise<void> {
    const segments = selectedRecordingID.value ? recordingTranscript.value : transcript.value;
    if (segments.length === 0) return;
    const lines = segments.map((segment) => {
      const at = formatDuration(Math.floor(segment.startOffsetMs / 1_000));
      return `- **${at}** ${segment.content}`;
    });
    try {
      await getDesktopBridge().saveMarkdown({
        suggestedName: `会议转写-${new Date().toISOString().slice(0, 10)}.md`,
        content: `# 会议转写\n\n${lines.join("\n")}`,
      });
    } catch (error) {
      meetingError.value = localError(error, "导出转写失败");
    }
  }

  async function exportSummary(): Promise<void> {
    const value = recordingSummary.value;
    if (!value) return;
    const renderList = (items: string[]) => items.length ? items.map((item) => `- ${item}`).join("\n") : "- 无";
    const actionItems = value.actionItems.length
      ? value.actionItems.map((item) => `- ${item.task}${item.assignee ? `（负责人：${item.assignee}）` : ""}${item.dueText ? `（期限：${item.dueText}）` : ""}`).join("\n")
      : "- 无";
    const content = `# ${value.topic}\n\n${value.abstract}\n\n## 讨论重点\n\n${renderList(value.keyDiscussions)}\n\n## 决策\n\n${renderList(value.decisions)}\n\n## 行动事项\n\n${actionItems}\n\n## 风险与待确认\n\n${renderList(value.risks)}\n`;
    try {
      await getDesktopBridge().saveMarkdown({ suggestedName: `${value.topic}.md`, content });
    } catch (error) {
      meetingError.value = localError(error, "导出会议总结失败");
    }
  }

  async function retryFailedAction(): Promise<void> {
    meetingError.value = undefined;
    if (!engineStatus.value?.modelDownloaded) {
      await downloadModel();
    } else {
      await startMeeting();
    }
  }

  function dismissError(): void { meetingError.value = undefined; }

  function upsertTranscriptSegment(segment: TranscriptSegment): void {
    transcript.value.push(segment);
    transcript.value.sort((left, right) => left.sequenceNo - right.sequenceNo);
  }

  function clearRecordingSelection(): void {
    if (recordingPlaybackURL.value) URL.revokeObjectURL(recordingPlaybackURL.value);
    recordingPlaybackURL.value = undefined;
    selectedRecordingID.value = undefined;
    recordingTranscript.value = [];
    recordingSummary.value = undefined;
    summaryError.value = undefined;
    recordingDetailError.value = undefined;
    recordingDetailLoading.value = false;
  }

  function resetMeetingView(): void {
    clearElapsedTimer();
    transcript.value = [];
    elapsedSeconds.value = 0;
    meetingError.value = undefined;
    systemAudioLevel.value = 0;
    mixedAudioLevel.value = 0;
    systemAudioSignalSeen.value = false;
  }

  function clearElapsedTimer(): void {
    if (elapsedTimer !== undefined) window.clearInterval(elapsedTimer);
    elapsedTimer = undefined;
  }

  function handlePageHide(): void {
    localTranscriber?.close();
    void recorder?.stop().catch((error: unknown) => console.error("stop recorder on page hide", error));
  }

  function handleLifecycleEvent(event: MeetingPluginLifecycleEvent): void {
    if (event.type === "enter") return;
    handlePageHide();
  }

  return {
    phase, session, transcript, elapsedSeconds, meetingError,
    captureSystemAudio, captureMicrophone, systemAudioLevel, mixedAudioLevel,
    systemAudioSignalSeen, runtimeInfo, recordings, recordingsLoading,
    recordingDirectory, selectedRecordingID, recordingPlaybackURL,
    recordingTranscript, recordingDetailLoading, recordingDetailError,
    recordingSummary, summaryGenerating, summaryError,
    summaryProvider, ollamaModels, ollamaChecking, ollamaBaseURL, ollamaModel,
    openAIModel, openAIApiKey,
    deepSeekModel, deepSeekApiKey, summaryCustomPrompt,
    engineStatus, engineLoading, engineSetupState, modelDownloadProgress, selectedTranscriptionModelId,
    modelDownloadReceivedBytes, modelDownloadTotalBytes,
    canStart, canStop, statusText, durationText,
    initializeRuntime, disposeRuntime, refreshEngineStatus, downloadModel, selectTranscriptionModel,
    startMeeting, stopMeeting, refreshRecordings, selectRecording, refreshOllamaModels,
    generateRecordingSummary, deleteRecording, exportTranscript, exportSummary,
    retryFailedAction, dismissError,
  };
});

function localError(error: unknown, title: string, retryable = false): MeetingErrorInfo {
  const mapped = toMeetingError(error);
  return { ...mapped, title, retryable, failedAction: retryable ? "start" : undefined };
}

function formatDuration(totalSeconds: number): string {
  const minutes = Math.floor(Math.max(0, totalSeconds) / 60);
  const seconds = Math.max(0, totalSeconds) % 60;
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

const statusLabels: Record<ClientMeetingPhase, string> = {
  idle: "准备开始",
  starting: "正在加载本地模型",
  recording: "本地转写中",
  stopping: "正在停止录音",
  processing: "正在完成本地转写",
  completed: "已保存到本机",
  partially_completed: "已部分完成",
  failed: "处理失败",
  cancelled: "已取消",
};

function readSummaryProvider(): MeetingSummaryProvider {
  const stored = window.localStorage.getItem("tiehu.meeting.summary-provider");
  if (stored === "openai" || stored === "deepseek") return stored;
  if (stored === "openai-compatible") return "deepseek";
  return "ollama";
}

function readTranscriptionModelID(): LocalTranscriptionModelID {
  const stored = window.localStorage.getItem("tiehu.meeting.transcription-model");
  switch (stored) {
    case "tiny-q5_1":
    case "base-q5_1":
    case "small-q5_1":
    case "medium-q5_0":
      return stored;
    default:
      return "small-q5_1";
  }
}

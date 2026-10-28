<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";

import ErrorBanner from "@/components/ErrorBanner.vue";
import LocalSetupCard from "@/components/LocalSetupCard.vue";
import MeetingControls from "@/components/MeetingControls.vue";
import RecordingList from "@/components/RecordingList.vue";
import SummarySettingsDialog from "@/components/SummarySettingsDialog.vue";
import TranscriptPanel from "@/components/TranscriptPanel.vue";
import { useMeetingStore } from "@/stores/meeting";

const meeting = useMeetingStore();
const logoURL = `${import.meta.env.BASE_URL}assets/logo.png`;
const summarySettingsOpen = ref(false);
const {
  phase,
  transcript,
  meetingError,
  captureSystemAudio,
  captureMicrophone,
  systemAudioLevel,
  mixedAudioLevel,
  systemAudioSignalSeen,
  runtimeInfo,
  recordings,
  recordingsLoading,
  recordingDirectory,
  selectedRecordingID,
  recordingPlaybackURL,
  recordingTranscript,
  recordingSummary,
  recordingDetailLoading,
  recordingDetailError,
  summaryGenerating,
  summaryError,
  summaryProvider,
  ollamaModels,
  ollamaChecking,
  ollamaBaseURL,
  ollamaModel,
  openAIModel,
  openAIApiKey,
  deepSeekModel,
  deepSeekApiKey,
  summaryCustomPrompt,
  canStart,
  canStop,
  statusText,
  durationText,
  engineStatus,
  engineLoading,
  selectedTranscriptionModelId,
  engineSetupState,
  modelDownloadProgress,
  modelDownloadReceivedBytes,
  modelDownloadTotalBytes,
} = storeToRefs(meeting);

const environmentText = computed(() => {
  if (runtimeInfo.value.platform === "browser") return "浏览器预览";
  return `本机运行 · ${runtimeInfo.value.platform}`;
});
const systemAudioAvailable = computed(
  () => runtimeInfo.value.windowType !== "browser" && runtimeInfo.value.platform !== "browser",
);
const summaryMethodLabel = computed(() => {
  if (summaryProvider.value === "ollama") return "本地 Ollama";
  return summaryProvider.value === "openai" ? "OpenAI" : "DeepSeek";
});
const summaryConfigured = computed(() => {
  if (summaryProvider.value === "ollama") return Boolean(ollamaModel.value.trim());
  if (summaryProvider.value === "openai") {
    return Boolean(openAIModel.value.trim() && openAIApiKey.value.trim());
  }
  return Boolean(deepSeekModel.value.trim() && deepSeekApiKey.value.trim());
});
const privacyModeText = computed(() => summaryProvider.value === "ollama" ? "完全本地模式" : "本地转写 · 云端总结");

onMounted(() => {
  meeting.initializeRuntime();
});
onBeforeUnmount(() => meeting.disposeRuntime());
</script>

<template>
  <main class="app-shell">
    <header class="app-header">
      <div class="brand-mark" aria-hidden="true">
        <img :src="logoURL" alt="" />
      </div>
      <div>
        <h1>会议助手</h1>
        <p>本地录音与转写，私有、安全、由你掌控。</p>
      </div>
      <div class="header-statuses">
        <span class="environment-badge">{{ environmentText }}</span>
        <span class="privacy-mode-badge" :class="{ cloud: summaryProvider !== 'ollama' }">{{ privacyModeText }}</span>
      </div>
      <button class="header-settings-button" type="button" @click="summarySettingsOpen = true">设置</button>
    </header>

    <ErrorBanner
      v-if="meetingError && meetingError.code !== 'LOCAL_MODEL_DOWNLOAD_FAILED'"
      :error="meetingError"
      @retry="meeting.retryFailedAction"
      @dismiss="meeting.dismissError"
    />

    <LocalSetupCard
      :state="engineSetupState"
      :model-name="engineStatus?.modelName"
      :model-size-bytes="engineStatus?.modelSizeBytes"
      :models="engineStatus?.models ?? []"
      :selected-model-id="selectedTranscriptionModelId"
      :received-bytes="modelDownloadReceivedBytes"
      :total-bytes="modelDownloadTotalBytes"
      :progress-percent="modelDownloadProgress"
      :error-message="meetingError?.code === 'LOCAL_MODEL_DOWNLOAD_FAILED' ? meetingError.message : undefined"
      @download="meeting.downloadModel"
      @retry="meeting.downloadModel"
      @select-model="meeting.selectTranscriptionModel"
    />

    <div class="dashboard-grid">
      <MeetingControls
        v-model:capture-system-audio="captureSystemAudio"
        v-model:capture-microphone="captureMicrophone"
        :status-text="statusText"
        :duration-text="durationText"
        :is-recording="phase === 'recording'"
        :can-start="canStart"
        :can-stop="canStop"
        :system-audio-level="systemAudioLevel"
        :mixed-audio-level="mixedAudioLevel"
        :system-audio-signal-seen="systemAudioSignalSeen"
        :system-audio-available="systemAudioAvailable"
        @start="meeting.startMeeting"
        @stop="meeting.stopMeeting"
      />
      <TranscriptPanel :segments="transcript" :is-recording="phase === 'recording'" />
    </div>

    <RecordingList
      :recordings="recordings"
      :loading="recordingsLoading"
      :storage-directory="recordingDirectory"
      :selected-recording-i-d="selectedRecordingID"
      :playback-u-r-l="recordingPlaybackURL"
      :transcript="recordingTranscript"
      :summary="recordingSummary"
      :detail-loading="recordingDetailLoading"
      :detail-error="recordingDetailError"
      :summary-generating="summaryGenerating"
      :summary-error="summaryError"
      :summary-method-label="summaryMethodLabel"
      :summary-configured="summaryConfigured"
      @select="meeting.selectRecording"
      @delete="meeting.deleteRecording"
      @export-transcript="meeting.exportTranscript"
      @configure-summary="summarySettingsOpen = true"
      @generate-summary="meeting.generateRecordingSummary"
      @export-summary="meeting.exportSummary"
    />

    <SummarySettingsDialog
      v-if="summarySettingsOpen"
      v-model:summary-provider="summaryProvider"
      v-model:ollama-base-u-r-l="ollamaBaseURL"
      v-model:ollama-model="ollamaModel"
      v-model:open-a-i-model="openAIModel"
      v-model:open-a-i-api-key="openAIApiKey"
      v-model:deep-seek-model="deepSeekModel"
      v-model:deep-seek-api-key="deepSeekApiKey"
      v-model:summary-custom-prompt="summaryCustomPrompt"
      :ollama-models="ollamaModels"
      :ollama-checking="ollamaChecking"
      :engine-status="engineStatus"
      :engine-loading="engineLoading"
      :selected-model-id="selectedTranscriptionModelId"
      :model-download-progress="modelDownloadProgress"
      @refresh-ollama="meeting.refreshOllamaModels"
      @download-model="meeting.downloadModel"
      @select-model="meeting.selectTranscriptionModel"
      @close="summarySettingsOpen = false"
    />

    <footer>
      <span>录音、转写与会议文件默认只留在本机</span>
      <span>·</span>
      <span>{{ summaryProvider === "ollama" ? "本地总结不会离开设备" : `使用 ${summaryMethodLabel} 时仅发送转写文字` }}</span>
    </footer>
  </main>
</template>

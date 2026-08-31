<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";

import ErrorBanner from "@/components/ErrorBanner.vue";
import MeetingControls from "@/components/MeetingControls.vue";
import RecordingList from "@/components/RecordingList.vue";
import SummaryPanel from "@/components/SummaryPanel.vue";
import TranscriptPanel from "@/components/TranscriptPanel.vue";
import WebAuthPanel from "@/components/WebAuthPanel.vue";
import { appConfig } from "@/config";
import {
  authenticateBrowser,
  requiresBrowserAuthentication,
} from "@/application/runtime";
import { useMeetingStore } from "@/stores/meeting";

const meeting = useMeetingStore();
const logoURL = `${import.meta.env.BASE_URL}assets/logo.png`;
const browserAuthenticationRequired = requiresBrowserAuthentication();
const authenticatedNickname = ref<string>();
const authenticationLoading = ref(false);
const authenticationError = ref<string>();
const {
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
} = storeToRefs(meeting);

const environmentText = computed(() => {
  const mode = appConfig.useMockApi ? "Mock" : "API";
  const audio = appConfig.useSyntheticAudio ? " · 合成 PCM" : "";
  const identity = authenticatedNickname.value ? ` · ${authenticatedNickname.value}` : "";
  return `${mode} · ${runtimeInfo.value.windowType}${audio}${identity}`;
});
const systemAudioAvailable = computed(
  () => runtimeInfo.value.windowType !== "browser" && runtimeInfo.value.platform !== "browser",
);
async function handleAuthentication(payload: {
  mode: "login" | "register";
  email: string;
  password: string;
  nickname?: string;
}): Promise<void> {
  authenticationLoading.value = true;
  authenticationError.value = undefined;
  try {
    const user = await authenticateBrowser(payload.mode, payload);
    authenticatedNickname.value = user.nickname;
    await meeting.refreshQuota();
  } catch (error) {
    authenticationError.value = error instanceof Error ? error.message : "登录失败，请重试";
  } finally {
    authenticationLoading.value = false;
  }
}

onMounted(() => meeting.initializeRuntime());
onBeforeUnmount(() => meeting.disposeRuntime());
</script>

<template>
  <main class="app-shell">
    <header class="app-header">
      <div class="brand-mark" aria-hidden="true">
        <img :src="logoURL" alt="" />
      </div>
      <div>
        <h1>AI 会议助手</h1>
        <p>让每一次讨论都有清晰结果</p>
      </div>
      <span class="environment-badge">{{ environmentText }}</span>
    </header>

    <WebAuthPanel
      v-if="browserAuthenticationRequired && !authenticatedNickname"
      :loading="authenticationLoading"
      :error="authenticationError"
      @authenticate="handleAuthentication"
    />

    <ErrorBanner
      v-else-if="meetingError"
      :error="meetingError"
      @retry="meeting.retryFailedAction"
      @dismiss="meeting.dismissError"
    />

    <div v-if="!browserAuthenticationRequired || authenticatedNickname" class="dashboard-grid">
      <MeetingControls
        v-model:retain-audio="retainAudio"
        v-model:capture-system-audio="captureSystemAudio"
        v-model:capture-microphone="captureMicrophone"
        v-model:transcription-consent="transcriptionConsent"
        :status-text="statusText"
        :duration-text="durationText"
        :is-recording="phase === 'recording'"
        :can-start="canStart"
        :can-stop="canStop"
        :connection-status-text="connectionStatusText"
        :show-connection-status="phase === 'starting' || phase === 'recording' || connectionState === 'disconnected'"
        :system-audio-level="systemAudioLevel"
        :mixed-audio-level="mixedAudioLevel"
        :system-audio-signal-seen="systemAudioSignalSeen"
        :system-audio-available="systemAudioAvailable"
        :quota="quota"
        :quota-loading="quotaLoading"
        :quota-error="quotaError"
        @start="meeting.startMeeting"
        @stop="meeting.stopMeeting"
        @refresh-quota="meeting.refreshQuota"
      />
      <TranscriptPanel :segments="transcript" :is-recording="phase === 'recording'" />
    </div>

    <RecordingList
      v-if="!browserAuthenticationRequired || authenticatedNickname"
      :recordings="recordings"
      :loading="recordingsLoading"
      :storage-directory="recordingDirectory"
      :selected-recording-i-d="selectedRecordingID"
      :playback-u-r-l="recordingPlaybackURL"
      :transcript="recordingTranscript"
      :meeting-status="recordingMeetingStatus"
      :summary="recordingSummary"
      :summary-status="recordingSummaryStatus"
      :summary-failure="recordingSummaryFailure"
      :summary-regenerating="recordingSummaryRegenerating"
      :detail-loading="recordingDetailLoading"
      :detail-error="recordingDetailError"
      @select="meeting.selectRecording"
      @delete="meeting.deleteRecording"
      @regenerate-summary="meeting.regenerateRecordingSummary"
    />

    <SummaryPanel v-if="(!browserAuthenticationRequired || authenticatedNickname) && summary" :summary="summary" @export="meeting.exportMarkdown" />

    <footer>
      <span>音频传输受加密连接保护</span>
      <span>·</span>
      <span>你可以随时删除云端会议数据</span>
    </footer>
  </main>
</template>

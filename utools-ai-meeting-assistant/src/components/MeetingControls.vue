<script setup lang="ts">
import { computed } from "vue";

import { getDisplayRemainingQuotaSeconds, type MeetingQuota } from "@/domain/meeting";

import QuotaCard from "./QuotaCard.vue";

const props = defineProps<{
  statusText: string;
  durationText: string;
  isRecording: boolean;
  canStart: boolean;
  canStop: boolean;
  connectionStatusText: string;
  showConnectionStatus: boolean;
  systemAudioLevel: number;
  mixedAudioLevel: number;
  systemAudioSignalSeen: boolean;
  systemAudioAvailable: boolean;
  quota?: MeetingQuota;
  quotaLoading: boolean;
  quotaError?: string;
}>();

const retainAudio = defineModel<boolean>("retainAudio", { required: true });
const captureSystemAudio = defineModel<boolean>("captureSystemAudio", { required: true });
const captureMicrophone = defineModel<boolean>("captureMicrophone", { required: true });
const transcriptionConsent = defineModel<boolean>("transcriptionConsent", { required: true });
const captureSourceSelected = computed(
  () => (captureSystemAudio.value && props.systemAudioAvailable) || captureMicrophone.value,
);
const systemAudioLevelPercent = computed(() => toMeterPercent(props.systemAudioLevel));
const mixedAudioLevelPercent = computed(() => toMeterPercent(props.mixedAudioLevel));
const systemAudioStatus = computed(() => {
  if (props.systemAudioLevel >= 0.0001) {
    return "已检测到电脑声音";
  }
  return props.systemAudioSignalSeen ? "电脑当前静音" : "尚未检测到电脑声音";
});
const quotaExhausted = computed(() =>
  props.quota ? getDisplayRemainingQuotaSeconds(props.quota) === 0 : false,
);
const anotherMeetingActive = computed(() => (props.quota?.activeMeetings ?? 0) > 0);

defineEmits<{
  start: [];
  stop: [];
  refreshQuota: [];
}>();

function toMeterPercent(peak: number): number {
  if (!Number.isFinite(peak) || peak <= 0) {
    return 0;
  }
  const decibels = 20 * Math.log10(Math.min(1, peak));
  return Math.max(0, Math.min(100, Math.round(((decibels + 60) / 60) * 100)));
}
</script>

<template>
  <section class="meeting-card controls-card" aria-labelledby="meeting-controls-title">
    <div class="status-row">
      <span class="status-dot" :class="{ active: isRecording }" aria-hidden="true"></span>
      <div>
        <p id="meeting-controls-title" class="eyebrow">当前状态</p>
        <h2>{{ statusText }} <span v-if="isRecording" class="timer">{{ durationText }}</span></h2>
        <span v-if="showConnectionStatus" class="connection-status">{{ connectionStatusText }}</span>
      </div>
    </div>

    <div v-if="isRecording" class="audio-levels" aria-live="polite">
      <div v-if="captureSystemAudio" class="audio-level-row">
        <div class="audio-level-heading">
          <strong>电脑音频</strong>
          <span :class="{ detected: systemAudioLevelPercent > 0 }">{{ systemAudioStatus }}</span>
        </div>
        <div class="audio-level-track" role="meter" aria-label="电脑系统音频电平" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="systemAudioLevelPercent">
          <span :style="{ width: `${systemAudioLevelPercent}%` }"></span>
        </div>
      </div>
      <div class="audio-level-row">
        <div class="audio-level-heading">
          <strong>发送音频</strong>
          <span>{{ mixedAudioLevelPercent > 0 ? "正在发送声音" : "当前静音" }}</span>
        </div>
        <div class="audio-level-track mixed" role="meter" aria-label="发送到实时转写的混合音频电平" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="mixedAudioLevelPercent">
          <span :style="{ width: `${mixedAudioLevelPercent}%` }"></span>
        </div>
      </div>
    </div>

    <QuotaCard
      :quota="quota"
      :loading="quotaLoading"
      :error="quotaError"
      @refresh="$emit('refreshQuota')"
    />

    <div class="option-list">
      <label class="option-row consent-option">
        <input v-model="transcriptionConsent" type="checkbox" :disabled="!canStart" />
        <span>
          <strong>同意云端语音转写</strong>
          <small>会议音频会临时传输到云端 ASR；这是开始会议的必要条件</small>
        </span>
      </label>
      <label class="option-row" :class="{ 'disabled-option': !systemAudioAvailable }">
        <input v-model="captureSystemAudio" type="checkbox" :disabled="!canStart || !systemAudioAvailable" />
        <span>
          <strong>录制电脑系统音频</strong>
          <small v-if="systemAudioAvailable">录入会议软件、浏览器和播放器的声音；macOS 需要“屏幕与系统音频录制”权限</small>
          <small v-else>普通浏览器无法采集系统音频，请从 uTools 开发者工具打开本插件</small>
        </span>
      </label>
      <label class="option-row">
        <input v-model="captureMicrophone" type="checkbox" :disabled="!canStart" />
        <span>
          <strong>同时录制麦克风</strong>
          <small>与电脑系统音频混音，记录你自己的发言</small>
        </span>
      </label>
      <label class="option-row">
        <input v-model="retainAudio" type="checkbox" :disabled="!canStart" />
        <span>
          <strong>保留云端录音</strong>
          <small>关闭时仅临时传输给 ASR，处理后删除原始音频</small>
        </span>
      </label>
    </div>

    <button
      v-if="canStart"
      class="primary-button"
      type="button"
      :disabled="!transcriptionConsent || !captureSourceSelected || quotaExhausted || anotherMeetingActive"
      @click="$emit('start')"
    >
      <template v-if="anotherMeetingActive">上一场会议正在结束</template>
      <template v-else-if="quotaExhausted">本月额度已用完</template>
      <template v-else><span class="button-icon">●</span> 开始会议</template>
    </button>
    <button v-else-if="canStop" class="stop-button" type="button" @click="$emit('stop')">
      <span class="stop-icon"></span> 停止会议
    </button>
    <button v-else class="stop-button" type="button" disabled>
      {{ statusText }}
    </button>
  </section>
</template>

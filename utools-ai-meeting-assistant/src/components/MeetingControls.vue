<script setup lang="ts">
import { computed } from "vue";

const props = defineProps<{
  statusText: string;
  durationText: string;
  isRecording: boolean;
  canStart: boolean;
  canStop: boolean;
  systemAudioLevel: number;
  mixedAudioLevel: number;
  systemAudioSignalSeen: boolean;
  systemAudioAvailable: boolean;
}>();

const captureSystemAudio = defineModel<boolean>("captureSystemAudio", { required: true });
const captureMicrophone = defineModel<boolean>("captureMicrophone", { required: true });
const captureSourceSelected = computed(
  () => (captureSystemAudio.value && props.systemAudioAvailable) || captureMicrophone.value,
);
const systemAudioLevelPercent = computed(() => toMeterPercent(props.systemAudioLevel));
const mixedAudioLevelPercent = computed(() => toMeterPercent(props.mixedAudioLevel));
const systemAudioStatus = computed(() => {
  if (props.systemAudioLevel >= 0.0001) return "已检测到电脑声音";
  return props.systemAudioSignalSeen ? "电脑当前静音" : "尚未检测到电脑声音";
});

defineEmits<{ start: []; stop: [] }>();

function toMeterPercent(peak: number): number {
  if (!Number.isFinite(peak) || peak <= 0) return 0;
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
        <span class="connection-status">本机处理 · 音频不上传</span>
      </div>
    </div>

    <div v-if="isRecording" class="audio-levels" aria-live="polite">
      <div v-if="captureSystemAudio" class="audio-level-row">
        <div class="audio-level-heading"><strong>电脑音频</strong><span>{{ systemAudioStatus }}</span></div>
        <div class="audio-level-track" role="meter" aria-label="电脑系统音频电平" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="systemAudioLevelPercent">
          <span :style="{ width: `${systemAudioLevelPercent}%` }"></span>
        </div>
      </div>
      <div class="audio-level-row">
        <div class="audio-level-heading"><strong>本地识别音频</strong><span>{{ mixedAudioLevelPercent > 0 ? "正在识别声音" : "当前静音" }}</span></div>
        <div class="audio-level-track mixed" role="meter" aria-label="本地识别音频电平" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="mixedAudioLevelPercent">
          <span :style="{ width: `${mixedAudioLevelPercent}%` }"></span>
        </div>
      </div>
    </div>

    <div class="option-list">
      <label class="option-row" :class="{ 'disabled-option': !systemAudioAvailable }">
        <input v-model="captureSystemAudio" type="checkbox" :disabled="!canStart || !systemAudioAvailable" />
        <span><strong>录制电脑系统音频</strong><small v-if="systemAudioAvailable">录入会议软件、浏览器和播放器声音；macOS 首次使用需授权系统音频录制</small><small v-else>请在 uTools 桌面端使用系统音频功能</small></span>
      </label>
      <label class="option-row">
        <input v-model="captureMicrophone" type="checkbox" :disabled="!canStart" />
        <span><strong>同时录制麦克风</strong><small>与电脑声音在本机混音，记录你自己的发言</small></span>
      </label>
    </div>

    <button v-if="canStop" class="stop-button" type="button" @click="$emit('stop')">
      <span class="stop-icon"></span> {{ isRecording ? "停止并保存" : "取消启动" }}
    </button>
    <button v-else class="primary-button" type="button" :disabled="!canStart || !captureSourceSelected" @click="$emit('start')">
      <span class="button-icon">●</span> {{ canStart ? "开始本地会议" : statusText }}
    </button>
  </section>
</template>

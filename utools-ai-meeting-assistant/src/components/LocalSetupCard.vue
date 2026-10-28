<script setup lang="ts">
import { computed } from "vue";

import type { LocalEngineSetupState } from "@/application/localEngine";

const props = defineProps<{
  state: LocalEngineSetupState;
  modelName?: string;
  modelSizeBytes?: number;
  models: LocalTranscriptionModelStatus[];
  selectedModelId: LocalTranscriptionModelID;
  receivedBytes: number;
  totalBytes: number;
  progressPercent: number;
  errorMessage?: string;
}>();

defineEmits<{ download: []; retry: []; selectModel: [modelId: LocalTranscriptionModelID] }>();

const modelSize = computed(() => formatBytes(props.modelSizeBytes ?? 190_085_487));
const progressDetail = computed(() => {
  if (props.totalBytes <= 0) return formatBytes(props.receivedBytes);
  return `${formatBytes(props.receivedBytes)} / ${formatBytes(props.totalBytes)}`;
});

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "未知大小";
  const megabytes = value / 1_048_576;
  return `${megabytes >= 10 ? Math.round(megabytes) : megabytes.toFixed(1)} MB`;
}
</script>

<template>
  <section class="meeting-card local-setup-card" :class="`setup-${state}`" aria-labelledby="local-setup-title">
    <div class="local-setup-copy">
      <p class="eyebrow">本地语音能力</p>
      <template v-if="state === 'checking'">
        <h2 id="local-setup-title">正在检查本地环境</h2>
        <p>正在确认语音模型和运行组件。</p>
      </template>
      <template v-else-if="state === 'unsupported'">
        <h2 id="local-setup-title">请在 uTools 桌面端使用</h2>
        <p>本地录音和语音识别支持 Windows 与 macOS。</p>
      </template>
      <template v-else-if="state === 'runtime_missing'">
        <h2 id="local-setup-title">缺少本地运行组件</h2>
        <p>请安装包含本地语音引擎的完整插件版本。</p>
      </template>
      <template v-else-if="state === 'model_required'">
        <h2 id="local-setup-title">选择并下载本地语音模型</h2>
        <p>不同模型的速度、准确率和体积不同。模型只需下载一次，之后可以离线转写。</p>
      </template>
      <template v-else-if="state === 'downloading'">
        <h2 id="local-setup-title">正在下载语音模型</h2>
        <p>{{ progressDetail }}，请保持插件开启。</p>
      </template>
      <template v-else-if="state === 'verifying'">
        <h2 id="local-setup-title">正在校验模型完整性</h2>
        <p>校验通过后即可使用本地语音识别。</p>
      </template>
      <template v-else-if="state === 'failed'">
        <h2 id="local-setup-title">模型下载未完成</h2>
        <p>{{ errorMessage || "请检查网络后重试，已下载的不完整文件不会被使用。" }}</p>
      </template>
      <template v-else>
        <h2 id="local-setup-title">本地转写已就绪</h2>
        <p>录音和语音识别在这台电脑上完成，会议音频不会上传。</p>
      </template>
    </div>

    <div v-if="state === 'model_required'" class="transcription-model-options local-model-options" role="radiogroup" aria-label="选择本地语音模型">
      <button
        v-for="model in models"
        :key="model.id"
        type="button"
        role="radio"
        :aria-checked="selectedModelId === model.id"
        :class="{ active: selectedModelId === model.id }"
        @click="$emit('selectModel', model.id)"
      >
        <span><strong>{{ model.name.replace('Whisper ', '') }}</strong><em v-if="model.recommended">推荐</em><em v-if="model.downloaded" class="downloaded">已下载</em></span>
        <small>{{ formatBytes(model.sizeBytes) }} · {{ model.description }}</small>
      </button>
    </div>

    <div v-if="state === 'downloading' || state === 'verifying'" class="local-setup-progress">
      <div class="model-progress" role="progressbar" aria-label="本地语音模型准备进度" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="progressPercent">
        <span :style="{ width: `${progressPercent}%` }"></span>
      </div>
      <strong>{{ state === "verifying" ? "正在校验" : `${progressPercent}%` }}</strong>
    </div>

    <div class="local-setup-actions">
      <button v-if="state === 'model_required'" class="primary-button local-setup-primary" type="button" @click="$emit('download')">
        下载 {{ modelName || "语音模型" }} · {{ modelSize }}
      </button>
      <button v-else-if="state === 'failed'" class="primary-button local-setup-primary" type="button" @click="$emit('retry')">重新下载</button>
      <span v-else-if="state === 'ready'" class="local-ready-label">本地模式</span>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from "vue";

import type { MeetingSummaryProvider } from "@/domain/meeting";

const props = defineProps<{
  ollamaModels: string[];
  ollamaChecking: boolean;
  engineStatus?: LocalTranscriptionStatus;
  selectedModelId: LocalTranscriptionModelID;
  engineLoading: boolean;
  modelDownloadProgress: number;
}>();

const summaryProvider = defineModel<MeetingSummaryProvider>("summaryProvider", { required: true });
const ollamaBaseURL = defineModel<string>("ollamaBaseURL", { required: true });
const ollamaModel = defineModel<string>("ollamaModel", { required: true });
const openAIModel = defineModel<string>("openAIModel", { required: true });
const openAIApiKey = defineModel<string>("openAIApiKey", { required: true });
const deepSeekModel = defineModel<string>("deepSeekModel", { required: true });
const deepSeekApiKey = defineModel<string>("deepSeekApiKey", { required: true });
const summaryCustomPrompt = defineModel<string>("summaryCustomPrompt", { required: true });

defineEmits<{ close: []; refreshOllama: []; downloadModel: []; selectModel: [modelId: LocalTranscriptionModelID] }>();

const engineReady = computed(() =>
  props.engineStatus?.binaryAvailable === true && props.engineStatus.modelDownloaded,
);
const selectedModel = computed(() => props.engineStatus?.models.find((model) => model.id === props.selectedModelId));
const modelSizeMB = computed(() => formatBytes(selectedModel.value?.sizeBytes ?? props.engineStatus?.modelSizeBytes ?? 190_085_487));

function formatBytes(value: number): string {
  const megabytes = value / 1_048_576;
  return `${megabytes >= 10 ? Math.round(megabytes) : megabytes.toFixed(1)} MB`;
}
</script>

<template>
  <div class="settings-backdrop" role="presentation" @click.self="$emit('close')">
    <section class="settings-dialog" role="dialog" aria-modal="true" aria-labelledby="summary-settings-title">
      <div class="settings-dialog-heading">
        <div><p class="eyebrow">设置</p><h2 id="summary-settings-title">本地能力设置</h2></div>
        <button class="settings-close" type="button" aria-label="关闭设置" @click="$emit('close')">×</button>
      </div>

      <section class="settings-block" aria-labelledby="speech-model-title">
        <div class="settings-section-heading">
          <div><strong id="speech-model-title">本地语音识别</strong><small>选择适合当前设备的 Whisper 模型</small></div>
          <span class="settings-status" :class="{ ready: engineReady }">{{ engineReady ? "已就绪" : "未安装" }}</span>
        </div>
        <p class="settings-block-copy">模型保存在本机 uTools 数据目录，录音与转写过程不会上传音频。</p>
        <div class="transcription-model-options" role="radiogroup" aria-label="本地语音模型">
          <button
            v-for="model in engineStatus?.models ?? []"
            :key="model.id"
            type="button"
            role="radio"
            :aria-checked="selectedModelId === model.id"
            :class="{ active: selectedModelId === model.id }"
            :disabled="engineLoading"
            @click="$emit('selectModel', model.id)"
          >
            <span><strong>{{ model.name.replace('Whisper ', '') }}</strong><em v-if="model.recommended">推荐</em><em v-if="model.downloaded" class="downloaded">已下载</em></span>
            <small>{{ formatBytes(model.sizeBytes) }} · {{ model.description }}</small>
          </button>
        </div>
        <div v-if="engineLoading" class="model-progress" role="progressbar" aria-label="本地语音模型下载进度" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="modelDownloadProgress">
          <span :style="{ width: `${modelDownloadProgress}%` }"></span>
        </div>
        <button class="secondary-button settings-download-button" type="button" :disabled="engineLoading || engineReady || engineStatus?.supported === false" @click="$emit('downloadModel')">
          {{ engineLoading ? `正在下载 ${modelDownloadProgress}%` : (engineReady ? `${selectedModel?.name ?? '当前模型'}已就绪` : `下载 ${selectedModel?.name ?? '语音模型'} · ${modelSizeMB}`) }}
        </button>
        <p v-if="engineStatus?.binaryAvailable === false && engineStatus.supported" class="settings-warning">当前插件包缺少本地运行组件，请安装完整版本后使用。</p>
      </section>

      <section class="settings-block" aria-labelledby="ai-provider-title">
        <div class="settings-section-heading">
          <div><strong id="ai-provider-title">AI 总结模型</strong><small>本地模式优先，也可以主动连接 OpenAI 或 DeepSeek</small></div>
        </div>
        <div class="summary-provider-options" role="radiogroup" aria-label="AI 总结方式">
          <button type="button" :class="{ active: summaryProvider === 'ollama' }" @click="summaryProvider = 'ollama'"><strong>Ollama</strong><small>完全本地，推荐</small></button>
          <button type="button" :class="{ active: summaryProvider === 'openai' }" @click="summaryProvider = 'openai'"><strong>OpenAI</strong><small>云端总结</small></button>
          <button type="button" :class="{ active: summaryProvider === 'deepseek' }" @click="summaryProvider = 'deepseek'"><strong>DeepSeek</strong><small>云端总结</small></button>
        </div>

        <div v-if="summaryProvider === 'ollama'" class="settings-fields settings-fields-flat">
          <label>服务地址<input v-model.trim="ollamaBaseURL" type="url" inputmode="url" autocomplete="off" placeholder="http://127.0.0.1:11434" /></label>
          <label>模型<input v-model.trim="ollamaModel" list="settings-ollama-model-options" autocomplete="off" placeholder="输入或选择本机模型" /><datalist id="settings-ollama-model-options"><option v-for="model in ollamaModels" :key="model" :value="model" /></datalist></label>
          <button class="secondary-button settings-check-button" type="button" :disabled="ollamaChecking" @click="$emit('refreshOllama')">{{ ollamaChecking ? "检测中…" : "检测本机模型" }}</button>
          <p class="summary-privacy-note local-note">会议文字只发送到本机回环地址。</p>
        </div>

        <div v-else-if="summaryProvider === 'openai'" class="settings-fields settings-fields-flat">
          <label>模型名称<input v-model.trim="openAIModel" autocomplete="off" placeholder="例如 gpt-4o-mini" /></label>
          <label>API Key<input v-model.trim="openAIApiKey" type="password" autocomplete="off" placeholder="仅保留在当前会话" /></label>
          <p class="summary-privacy-note cloud-note">生成总结时，会议转写会发送至 OpenAI；录音文件不会上传。</p>
        </div>

        <div v-else class="settings-fields settings-fields-flat">
          <label>模型名称<input v-model.trim="deepSeekModel" autocomplete="off" placeholder="例如 deepseek-flash" /></label>
          <label>API Key<input v-model.trim="deepSeekApiKey" type="password" autocomplete="off" placeholder="仅保留在当前会话" /></label>
          <p class="summary-privacy-note cloud-note">生成总结时，会议转写会发送至 DeepSeek；录音文件不会上传。</p>
        </div>
      </section>

      <section class="settings-block" aria-labelledby="prompt-title">
        <div class="settings-section-heading"><div><strong id="prompt-title">自定义提示词</strong><small>定义总结重点、语言、格式或角色</small></div></div>
        <label class="settings-custom-prompt">提示词<textarea v-model.trim="summaryCustomPrompt" maxlength="2000" rows="5" placeholder="例如：使用中文，优先提取关键结论、负责人和下一步行动。" /><small>{{ summaryCustomPrompt.length }}/2000</small></label>
      </section>

      <button class="primary-button" type="button" @click="$emit('close')">保存并完成</button>
    </section>
  </div>
</template>

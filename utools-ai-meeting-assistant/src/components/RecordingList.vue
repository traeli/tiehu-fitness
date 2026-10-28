<script setup lang="ts">
import { ref, watch } from "vue";
import type { LocalMeetingSummary, TranscriptSegment } from "@/domain/meeting";
import type { LocalMeetingRecording } from "@/infrastructure/recording/recordingRepository";

const props = defineProps<{
  recordings: LocalMeetingRecording[];
  loading: boolean;
  storageDirectory: string;
  selectedRecordingID?: string;
  playbackURL?: string;
  transcript: TranscriptSegment[];
  summary?: LocalMeetingSummary;
  detailLoading: boolean;
  detailError?: string;
  summaryGenerating: boolean;
  summaryError?: string;
  summaryMethodLabel: string;
  summaryConfigured: boolean;
}>();
const emit = defineEmits<{
  select: [recordingID: string];
  delete: [recordingID: string];
  exportTranscript: [];
  configureSummary: [];
  generateSummary: [];
  exportSummary: [];
}>();
const audioElement = ref<HTMLAudioElement>();
const activeTab = ref<"summary" | "transcript">("summary");

watch(() => props.selectedRecordingID, () => { activeTab.value = "summary"; });

function requestDelete(recordingID: string): void {
  if (window.confirm("确定删除这条本地录音、转写和总结吗？删除后无法恢复。")) emit("delete", recordingID);
}
function formatCreatedAt(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}
function formatDuration(durationMs: number): string {
  const seconds = Math.max(0, Math.round(durationMs / 1_000));
  return `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}
function formatSize(sizeBytes: number): string {
  return sizeBytes < 1_048_576 ? `${Math.max(1, Math.round(sizeBytes / 1_024))} KB` : `${(sizeBytes / 1_048_576).toFixed(1)} MB`;
}
function seekTo(offsetMs: number): void {
  if (!audioElement.value) return;
  audioElement.value.currentTime = Math.max(0, offsetMs / 1_000);
  void audioElement.value.play();
}
</script>

<template>
  <section class="meeting-card recording-card" aria-labelledby="recording-list-title">
    <div class="section-heading"><div><p class="eyebrow">仅保存在这台电脑</p><h2 id="recording-list-title">本地会议记录</h2></div><span class="recording-count">{{ recordings.length }}</span></div>
    <p class="recording-directory">本机目录：{{ storageDirectory }}</p>
    <p v-if="loading" class="recording-empty">正在读取本地记录…</p>
    <p v-else-if="recordings.length === 0" class="recording-empty">完成一次会议后，录音、转写与总结会保存在本机。</p>
    <div v-else class="recording-browser">
      <div class="recording-list" role="list">
        <article v-for="recording in recordings" :key="recording.id" class="recording-item" :class="{ selected: recording.id === selectedRecordingID }">
          <button class="recording-select" type="button" @click="emit('select', recording.id)"><strong>{{ formatCreatedAt(recording.createdAt) }}</strong><span>{{ formatDuration(recording.durationMs) }} · {{ formatSize(recording.sizeBytes) }}</span><small>本地会议 {{ recording.meetingId.slice(0, 8) }}</small></button>
          <button class="recording-delete" type="button" @click="requestDelete(recording.id)">删除</button>
        </article>
      </div>
      <section class="recording-detail">
        <div class="section-heading"><div><p class="eyebrow">会议详情</p><h3>录音、转写与总结</h3></div></div>
        <div v-if="!selectedRecordingID" class="recording-detail-empty">点击左侧会议，可回放录音、查看转写并生成本地总结。</div>
        <template v-else>
          <p v-if="detailError" class="recording-detail-error">{{ detailError }}</p>
          <p v-if="detailLoading" class="recording-detail-empty">正在读取本地文件…</p>
          <template v-else>
            <audio ref="audioElement" v-if="playbackURL" controls preload="metadata" :src="playbackURL">当前环境不支持音频回放。</audio>
            <div class="meeting-detail-tabs" role="tablist">
              <button type="button" :class="{ active: activeTab === 'summary' }" @click="activeTab = 'summary'">AI 总结 <small>{{ summary ? "已生成" : "本地" }}</small></button>
              <button type="button" :class="{ active: activeTab === 'transcript' }" @click="activeTab = 'transcript'">完整转写 <small>{{ transcript.length }} 段</small></button>
            </div>

            <div v-if="activeTab === 'summary'" class="history-summary">
              <section class="summary-action-card">
                <div><strong>AI 会议总结</strong><small>当前方式：{{ summaryMethodLabel }}</small></div>
                <p v-if="summaryError" class="recording-detail-error">{{ summaryError }}</p>
                <div class="summary-action-buttons"><button class="secondary-button" type="button" @click="emit('configureSummary')">打开配置</button><button class="primary-button" type="button" :disabled="summaryGenerating || !transcript.length || !summaryConfigured" @click="emit('generateSummary')">{{ summaryGenerating ? "正在生成…" : summary ? "重新生成" : "生成总结" }}</button></div>
              </section>

              <template v-if="summary">
                <div class="history-summary-heading"><div><h4>{{ summary.topic }}</h4><small>AI 总结 · {{ formatCreatedAt(summary.generatedAt) }}</small></div><button class="secondary-button" type="button" @click="emit('exportSummary')">导出 Markdown</button></div>
                <p class="summary-abstract">{{ summary.abstract }}</p>
                <section class="summary-section"><h5>讨论重点</h5><ul v-if="summary.keyDiscussions.length"><li v-for="item in summary.keyDiscussions" :key="item">{{ item }}</li></ul><small v-else>无明确内容</small></section>
                <section class="summary-section"><h5>决策</h5><ul v-if="summary.decisions.length"><li v-for="item in summary.decisions" :key="item">{{ item }}</li></ul><small v-else>无明确决策</small></section>
                <section class="summary-section"><h5>行动事项</h5><ul v-if="summary.actionItems.length"><li v-for="item in summary.actionItems" :key="`${item.task}-${item.assignee}-${item.dueText}`"><strong>{{ item.task }}</strong><span v-if="item.assignee || item.dueText"> · {{ item.assignee || "负责人未定" }}{{ item.dueText ? ` · ${item.dueText}` : "" }}</span></li></ul><small v-else>无明确行动事项</small></section>
                <section class="summary-section summary-risks"><h5>风险与待确认</h5><ul v-if="summary.risks.length"><li v-for="item in summary.risks" :key="item">{{ item }}</li></ul><small v-else>无明确风险</small></section>
              </template>
              <p v-else class="recording-detail-empty">完成 AI 总结配置后，可根据完整转写生成会议总结。</p>
            </div>

            <div v-else>
              <div class="history-transcript-actions"><button v-if="transcript.length" class="secondary-button" type="button" @click="emit('exportTranscript')">导出 Markdown</button></div>
              <div v-if="transcript.length" class="history-transcript-list">
                <button v-for="segment in transcript" :key="segment.id" type="button" class="transcript-item transcript-seek" @click="seekTo(segment.startOffsetMs)"><time>{{ formatDuration(segment.startOffsetMs) }}</time><span><strong>本地转写</strong><p>{{ segment.content }}</p></span></button>
              </div>
              <p v-else class="recording-detail-empty">该会议暂无转写内容。</p>
            </div>
          </template>
        </template>
      </section>
    </div>
  </section>
</template>

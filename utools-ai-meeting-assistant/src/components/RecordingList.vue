<script setup lang="ts">
import { ref, watch } from "vue";

import MeetingSummaryDetail from "@/components/MeetingSummaryDetail.vue";
import type {
  MeetingStatus,
  MeetingSummary,
  MeetingSummaryStatus,
  TranscriptSegment,
} from "@/domain/meeting";
import type { LocalMeetingRecording } from "@/infrastructure/recording/recordingRepository";

const props = defineProps<{
  recordings: LocalMeetingRecording[];
  loading: boolean;
  storageDirectory: string;
  selectedRecordingID?: string;
  playbackURL?: string;
  transcript: TranscriptSegment[];
  meetingStatus?: MeetingStatus;
  summary?: MeetingSummary;
  summaryStatus: MeetingSummaryStatus;
  summaryFailure?: string;
  summaryRegenerating: boolean;
  detailLoading: boolean;
  detailError?: string;
}>();

const emit = defineEmits<{
  select: [recordingID: string];
  delete: [recordingID: string];
  regenerateSummary: [];
}>();

const activeTab = ref<"summary" | "transcript">("transcript");
const audioElement = ref<HTMLAudioElement>();

watch(
  () => [props.selectedRecordingID, props.summaryStatus] as const,
  ([, summaryStatus]) => {
    activeTab.value = summaryStatus === "succeeded" ? "summary" : "transcript";
  },
);

const statusLabels: Record<MeetingStatus, string> = {
  recording: "录制中",
  processing: "处理中",
  completed: "已完成",
  partially_completed: "部分完成",
  failed: "处理失败",
  cancelled: "已取消",
};

function requestDelete(recordingID: string): void {
  if (window.confirm("确定删除这条本地录音吗？删除后无法恢复。")) {
    emit("delete", recordingID);
  }
}

function formatCreatedAt(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function formatDuration(durationMs: number): string {
  const totalSeconds = Math.max(0, Math.round(durationMs / 1_000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function formatOffset(offsetMs: number): string {
  const totalSeconds = Math.max(0, Math.floor(offsetMs / 1_000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function formatSize(sizeBytes: number): string {
  if (sizeBytes < 1_024 * 1_024) {
    return `${Math.max(1, Math.round(sizeBytes / 1_024))} KB`;
  }
  return `${(sizeBytes / (1_024 * 1_024)).toFixed(1)} MB`;
}

function seekTo(offsetMs: number): void {
  if (!audioElement.value) {
    return;
  }
  audioElement.value.currentTime = Math.max(0, offsetMs / 1_000);
  void audioElement.value.play();
}
</script>

<template>
  <section class="meeting-card recording-card" aria-labelledby="recording-list-title">
    <div class="section-heading">
      <div>
        <p class="eyebrow">LOCAL MEETINGS</p>
        <h2 id="recording-list-title">最近会议录音</h2>
      </div>
      <span class="recording-count">{{ recordings.length }}</span>
    </div>
    <p class="recording-directory">录音文件：{{ storageDirectory }}</p>

    <p v-if="loading" class="recording-empty">正在读取本地录音…</p>
    <p v-else-if="recordings.length === 0" class="recording-empty">
      使用系统音频或麦克风完成会议后，uTools 会把混音录音保存到上述本地文件夹。
    </p>
    <div v-else class="recording-browser">
      <div class="recording-list" role="list" aria-label="最近会议录音">
        <article
          v-for="recording in recordings"
          :key="recording.id"
          class="recording-item"
          :class="{ selected: recording.id === selectedRecordingID }"
          role="listitem"
        >
          <button class="recording-select" type="button" @click="emit('select', recording.id)">
            <strong>{{ formatCreatedAt(recording.createdAt) }}</strong>
            <span>{{ formatDuration(recording.durationMs) }} · {{ formatSize(recording.sizeBytes) }}</span>
            <small>会议 {{ recording.meetingId.slice(0, 8) }}</small>
          </button>
          <button class="recording-delete" type="button" @click="requestDelete(recording.id)">
            删除
          </button>
        </article>
      </div>

      <section class="recording-detail" aria-labelledby="recording-detail-title">
        <div class="section-heading">
          <div>
            <p class="eyebrow">MEETING DETAIL</p>
            <h3 id="recording-detail-title">会议内容</h3>
          </div>
          <span v-if="meetingStatus" class="meeting-status-badge">
            {{ statusLabels[meetingStatus] }}
          </span>
        </div>

        <div v-if="!selectedRecordingID" class="recording-detail-empty">
          点击左侧会议，可回放本地录音并查看 AI 总结与完整转写。
        </div>
        <template v-else>
          <p v-if="detailError" class="recording-detail-error">{{ detailError }}</p>
          <p v-if="detailLoading" class="recording-detail-empty">正在加载录音和会议内容…</p>
          <audio ref="audioElement" v-if="playbackURL" controls preload="metadata" :src="playbackURL">
            当前运行环境不支持音频回放。
          </audio>

          <div v-if="!detailLoading" class="meeting-detail-tabs" role="tablist">
            <button
              type="button"
              role="tab"
              :aria-selected="activeTab === 'summary'"
              :class="{ active: activeTab === 'summary' }"
              @click="activeTab = 'summary'"
            >
              AI 会议总结
              <span :class="`summary-dot status-${summaryStatus}`" />
            </button>
            <button
              type="button"
              role="tab"
              :aria-selected="activeTab === 'transcript'"
              :class="{ active: activeTab === 'transcript' }"
              @click="activeTab = 'transcript'"
            >
              完整转写
              <small>{{ transcript.length }}</small>
            </button>
          </div>

          <MeetingSummaryDetail
            v-if="!detailLoading && activeTab === 'summary'"
            :status="summaryStatus"
            :summary="summary"
            :failure-reason="summaryFailure"
            :regenerating="summaryRegenerating"
            @regenerate="emit('regenerateSummary')"
          />

          <template v-else-if="!detailLoading && activeTab === 'transcript'">
            <div v-if="transcript.length" class="history-transcript-list">
              <button
                v-for="segment in transcript"
                :key="segment.id"
                type="button"
                class="transcript-item transcript-seek"
                @click="seekTo(segment.startOffsetMs)"
              >
                <time>{{ formatOffset(segment.startOffsetMs) }}</time>
                <span>
                  <strong>{{ segment.speakerLabel || "发言人" }}</strong>
                  <p>{{ segment.content }}</p>
                </span>
              </button>
            </div>
            <p v-else class="recording-detail-empty">该会议暂无可用的最终转写内容。</p>
          </template>
        </template>
      </section>
    </div>
  </section>
</template>

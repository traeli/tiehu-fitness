<script setup lang="ts">
import type { TranscriptSegment } from "@/domain/meeting";

defineProps<{ segments: TranscriptSegment[]; isRecording: boolean }>();

function formatOffset(offsetMs: number): string {
  const totalSeconds = Math.max(0, Math.floor(offsetMs / 1_000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}
</script>

<template>
  <section class="meeting-card content-card" aria-labelledby="transcript-title">
    <div class="section-heading">
      <div>
        <p class="eyebrow">本地实时转写</p>
        <h2 id="transcript-title">实时会议内容</h2>
      </div>
      <span v-if="isRecording" class="live-badge">实时</span>
    </div>

    <div v-if="segments.length" class="transcript-list" aria-live="polite">
      <article v-for="segment in segments" :key="segment.id" class="transcript-item">
        <time>{{ formatOffset(segment.startOffsetMs) }}</time>
        <div>
          <strong>{{ segment.speakerLabel || "发言人" }}</strong>
          <p>{{ segment.content }}</p>
        </div>
      </article>
    </div>
    <div v-else class="empty-state">
      <span class="waveform" aria-hidden="true">▂▄▆▄▂</span>
      <p>{{ isRecording ? "正在等待语音，请确认会议声音或麦克风有输入。" : "开始会议后，转写内容会实时显示在这里。所有语音识别均在本机完成。" }}</p>
    </div>
  </section>
</template>

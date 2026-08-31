<script setup lang="ts">
import type {
  MeetingSummary,
  MeetingSummaryStatus,
} from "@/domain/meeting";

defineProps<{
  status: MeetingSummaryStatus;
  summary?: MeetingSummary;
  failureReason?: string;
  regenerating: boolean;
}>();

defineEmits<{ regenerate: [] }>();

const statusText: Record<MeetingSummaryStatus, string> = {
  not_started: "尚未生成会议总结",
  pending: "会议总结正在排队",
  processing: "正在生成会议总结",
  succeeded: "会议总结已生成",
  failed: "会议总结生成失败",
};

function formatGeneratedAt(value?: string): string {
  if (!value) {
    return "";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}
</script>

<template>
  <div class="history-summary">
    <div class="history-summary-status" :class="`status-${status}`">
      <span v-if="status === 'pending' || status === 'processing'" class="summary-spinner" />
      <strong>{{ statusText[status] }}</strong>
      <button
        v-if="status === 'failed' || status === 'not_started'"
        type="button"
        class="secondary-button"
        :disabled="regenerating"
        @click="$emit('regenerate')"
      >
        {{ regenerating ? "正在提交…" : status === "failed" ? "重新生成" : "生成总结" }}
      </button>
    </div>

    <p v-if="failureReason" class="summary-failure-reason">{{ failureReason }}</p>

    <template v-if="summary">
      <div class="history-summary-heading">
        <div>
          <p class="eyebrow">MEETING TOPIC</p>
          <h4>{{ summary.topic }}</h4>
        </div>
        <small v-if="summary.generatedAt">{{ formatGeneratedAt(summary.generatedAt) }}</small>
      </div>
      <p class="summary-abstract">{{ summary.abstract }}</p>

      <section v-if="summary.keyDiscussions.length" class="summary-section">
        <h5>关键讨论</h5>
        <ul><li v-for="item in summary.keyDiscussions" :key="item">{{ item }}</li></ul>
      </section>
      <section v-if="summary.decisions.length" class="summary-section">
        <h5>会议决策</h5>
        <ul><li v-for="item in summary.decisions" :key="item">{{ item }}</li></ul>
      </section>
      <section v-if="summary.actionItems.length" class="summary-section">
        <h5>待办事项</h5>
        <ul>
          <li v-for="item in summary.actionItems" :key="`${item.assignee}-${item.task}`">
            <strong>{{ item.assignee || "负责人待定" }}</strong>：{{ item.task }}
            <small v-if="item.dueText"> · {{ item.dueText }}</small>
          </li>
        </ul>
      </section>
      <section v-if="summary.risks.length" class="summary-section summary-risks">
        <h5>风险与待确认</h5>
        <ul><li v-for="item in summary.risks" :key="item">{{ item }}</li></ul>
      </section>

      <p v-if="summary.modelName" class="summary-meta">
        {{ summary.provider || "LLM" }} · {{ summary.modelName }}
        <template v-if="summary.version"> · 版本 {{ summary.version }}</template>
      </p>
    </template>
  </div>
</template>

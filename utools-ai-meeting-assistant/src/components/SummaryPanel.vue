<script setup lang="ts">
import type { MeetingSummary } from "@/domain/meeting";

defineProps<{ summary: MeetingSummary }>();
defineEmits<{ export: [] }>();
</script>

<template>
  <section class="meeting-card content-card summary-card" aria-labelledby="summary-title">
    <div class="section-heading">
      <div>
        <p class="eyebrow">AI SUMMARY</p>
        <h2 id="summary-title">{{ summary.topic }}</h2>
      </div>
      <button class="secondary-button" type="button" @click="$emit('export')">导出 Markdown</button>
    </div>
    <p class="summary-abstract">{{ summary.abstract }}</p>
    <div class="summary-grid">
      <div class="full-width" v-if="summary.keyDiscussions.length">
        <h3>关键讨论</h3>
        <ul><li v-for="item in summary.keyDiscussions" :key="item">{{ item }}</li></ul>
      </div>
      <div>
        <h3>核心决定</h3>
        <ul><li v-for="item in summary.decisions" :key="item">{{ item }}</li></ul>
      </div>
      <div>
        <h3>待办事项</h3>
        <ul>
          <li v-for="item in summary.actionItems" :key="`${item.assignee}-${item.task}`">
            <strong>{{ item.assignee || "待定" }}</strong>：{{ item.task }}
            <small v-if="item.dueText"> · {{ item.dueText }}</small>
          </li>
        </ul>
      </div>
      <div class="full-width">
        <h3>风险</h3>
        <ul><li v-for="item in summary.risks" :key="item">{{ item }}</li></ul>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from "vue";

import type { MeetingQuota } from "@/domain/meeting";

const props = defineProps<{
  quota?: MeetingQuota;
  loading: boolean;
  error?: string;
}>();

defineEmits<{ refresh: [] }>();

const usedPercent = computed(() => {
  if (!props.quota || props.quota.totalLimitSeconds <= 0) {
    return 0;
  }
  const occupied = props.quota.totalLimitSeconds - props.quota.remainingSeconds;
  return Math.max(0, Math.min(100, Math.round((occupied / props.quota.totalLimitSeconds) * 100)));
});

const resetText = computed(() => {
  if (!props.quota) {
    return "";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    month: "long",
    day: "numeric",
  }).format(new Date(props.quota.periodEnd));
});

function formatQuota(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) {
    return "0 分钟";
  }
  const wholeMinutes = Math.ceil(seconds / 60);
  const hours = Math.floor(wholeMinutes / 60);
  const minutes = wholeMinutes % 60;
  if (hours === 0) {
    return `${minutes} 分钟`;
  }
  return minutes === 0 ? `${hours} 小时` : `${hours} 小时 ${minutes} 分钟`;
}
</script>

<template>
  <section class="quota-card" aria-label="会议额度">
    <div class="quota-heading">
      <div>
        <p class="eyebrow">本月会议额度</p>
        <strong v-if="quota">剩余 {{ formatQuota(quota.remainingSeconds) }}</strong>
        <strong v-else-if="loading">正在加载额度…</strong>
        <strong v-else>额度暂不可用</strong>
      </div>
      <button v-if="error && !loading" type="button" @click="$emit('refresh')">重试</button>
    </div>

    <template v-if="quota">
      <div class="quota-track" role="progressbar" aria-label="本月额度使用进度" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="usedPercent">
        <span :style="{ width: `${usedPercent}%` }"></span>
      </div>
      <div class="quota-breakdown">
        <span>总额度 {{ formatQuota(quota.totalLimitSeconds) }}</span>
        <span>基础 {{ formatQuota(quota.baseLimitSeconds) }}</span>
        <span v-if="quota.purchasedLimitSeconds > 0">购买 {{ formatQuota(quota.purchasedLimitSeconds) }}</span>
      </div>
      <small>
        已使用 {{ formatQuota(quota.consumedSeconds) }}
        <template v-if="quota.reservedSeconds > 0"> · 会议预占 {{ formatQuota(quota.reservedSeconds) }}</template>
        · {{ resetText }}重置
      </small>
    </template>
    <p v-else-if="error" class="quota-error">{{ error }}</p>
  </section>
</template>

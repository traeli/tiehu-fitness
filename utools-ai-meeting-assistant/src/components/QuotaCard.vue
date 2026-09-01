<script setup lang="ts">
import { computed } from "vue";

import {
  formatQuotaDuration,
  getDisplayRemainingQuotaSeconds,
  type MeetingQuota,
} from "@/domain/meeting";

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
  const occupied = props.quota.totalLimitSeconds - getDisplayRemainingQuotaSeconds(props.quota);
  return Math.max(0, Math.min(100, Math.round((occupied / props.quota.totalLimitSeconds) * 100)));
});

const displayRemainingSeconds = computed(() =>
  props.quota ? getDisplayRemainingQuotaSeconds(props.quota) : 0,
);

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
</script>

<template>
  <section class="quota-card" aria-label="会议额度">
    <div class="quota-heading">
      <div>
        <p class="eyebrow">本月会议额度</p>
        <strong v-if="quota">剩余 {{ formatQuotaDuration(displayRemainingSeconds) }}</strong>
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
        <span>总额度 {{ formatQuotaDuration(quota.totalLimitSeconds) }}</span>
        <span>基础 {{ formatQuotaDuration(quota.baseLimitSeconds) }}</span>
        <span v-if="quota.purchasedLimitSeconds > 0">购买 {{ formatQuotaDuration(quota.purchasedLimitSeconds) }}</span>
      </div>
      <small>
        已使用 {{ formatQuotaDuration(quota.consumedSeconds) }}
        · {{ resetText }}重置
      </small>
      <p v-if="error" class="quota-error">{{ error }}，当前显示的是上次成功加载的数据。</p>
    </template>
    <p v-else-if="error" class="quota-error">{{ error }}</p>
  </section>
</template>

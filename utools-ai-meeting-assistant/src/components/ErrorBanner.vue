<script setup lang="ts">
import type { MeetingErrorInfo } from "@/application/meetingError";

defineProps<{ error: MeetingErrorInfo }>();
defineEmits<{ retry: []; dismiss: [] }>();

const retryLabels = {
  start: "重新开始",
  stop: "重试停止",
  export: "重新导出",
} as const;
</script>

<template>
  <aside class="error-banner" role="alert" aria-live="assertive">
    <div>
      <strong>{{ error.title }}</strong>
      <p>{{ error.message }}</p>
      <small>{{ error.code }}</small>
    </div>
    <div class="error-actions">
      <button
        v-if="error.retryable && error.failedAction"
        class="error-retry-button"
        type="button"
        @click="$emit('retry')"
      >
        {{ retryLabels[error.failedAction] }}
      </button>
      <button class="error-dismiss-button" type="button" aria-label="关闭错误提示" @click="$emit('dismiss')">
        ×
      </button>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { ref } from "vue";

defineProps<{
  loading: boolean;
  error?: string;
}>();

const emit = defineEmits<{
  authenticate: [payload: { mode: "login" | "register"; email: string; password: string; nickname?: string }];
}>();

const mode = ref<"login" | "register">("register");
const email = ref("");
const password = ref("");
const nickname = ref("");

function submit(): void {
  emit("authenticate", {
    mode: mode.value,
    email: email.value.trim(),
    password: password.value,
    nickname: mode.value === "register" ? nickname.value.trim() : undefined,
  });
}
</script>

<template>
  <section class="meeting-card auth-card" aria-labelledby="web-auth-title">
    <div>
      <p class="eyebrow">浏览器本地联调</p>
      <h2 id="web-auth-title">{{ mode === "register" ? "创建测试账号" : "登录测试账号" }}</h2>
      <p class="auth-description">登录后页面会自动创建会议、连接本地 WebSocket 并采集麦克风 PCM。</p>
    </div>

    <div class="auth-mode-tabs" role="tablist" aria-label="认证方式">
      <button type="button" :class="{ active: mode === 'register' }" @click="mode = 'register'">注册</button>
      <button type="button" :class="{ active: mode === 'login' }" @click="mode = 'login'">登录</button>
    </div>

    <form class="auth-form" @submit.prevent="submit">
      <label>
        <span>邮箱</span>
        <input v-model="email" name="email" type="email" autocomplete="email" required placeholder="local.user@example.com" />
      </label>
      <label>
        <span>密码</span>
        <input v-model="password" name="password" type="password" :autocomplete="mode === 'register' ? 'new-password' : 'current-password'" minlength="8" maxlength="72" required placeholder="至少 8 个字符" />
      </label>
      <label v-if="mode === 'register'">
        <span>昵称</span>
        <input v-model="nickname" name="nickname" type="text" maxlength="80" placeholder="本地测试用户" />
      </label>
      <p v-if="error" class="auth-error" role="alert">{{ error }}</p>
      <button class="primary-button" type="submit" :disabled="loading">
        {{ loading ? "正在连接…" : mode === "register" ? "注册并进入" : "登录并进入" }}
      </button>
    </form>
  </section>
</template>

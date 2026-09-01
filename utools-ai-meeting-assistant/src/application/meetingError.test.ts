import { describe, expect, it } from "vitest";

import { ApiError } from "@/infrastructure/api/apiClient";
import { AudioCaptureError } from "@/infrastructure/audio/microphoneRecorder";
import { RealtimeTranscriptionError } from "@/infrastructure/realtime/transcriptionClient";

import { toMeetingError } from "./meetingError";

describe("meeting error mapping", () => {
  it("maps quota errors to a stable non-retryable message", () => {
    const result = toMeetingError(
      new ApiError("provider detail", 429, "MEETING_QUOTA_EXCEEDED"),
      "start",
    );

    expect(result.code).toBe("MEETING_QUOTA_EXCEEDED");
    expect(result.retryable).toBe(false);
    expect(result.message).not.toContain("provider detail");
  });

  it("maps an active meeting conflict without degrading to unknown error", () => {
    const result = toMeetingError(
      new ApiError("too many active meetings", 429, "MEETING_CONCURRENT_LIMIT_REACHED"),
      "start",
    );

    expect(result.code).toBe("MEETING_CONCURRENT_LIMIT_REACHED");
    expect(result.title).toBe("已有会议尚未结束");
    expect(result.retryable).toBe(true);
  });

  it("explains missing uTools server authentication without exposing backend details", () => {
    const result = toMeetingError(
      new ApiError(
        "uTools authentication is not configured",
        503,
        "UTOOLS_AUTH_NOT_CONFIGURED",
      ),
      "start",
    );

    expect(result.code).toBe("UTOOLS_AUTH_NOT_CONFIGURED");
    expect(result.title).toBe("uTools 登录尚未配置");
    expect(result.retryable).toBe(false);
    expect(result.message).not.toContain("plugin_secret");
  });

  it("keeps export failures retryable", () => {
    const result = toMeetingError(new Error("disk is read-only"), "export");

    expect(result.code).toBe("EXPORT_FAILED");
    expect(result.failedAction).toBe("export");
    expect(result.retryable).toBe(true);
  });

  it("distinguishes system audio permission from microphone permission", () => {
    const result = toMeetingError(
      new AudioCaptureError(
        "SYSTEM_AUDIO_PERMISSION_DENIED",
        "请允许屏幕与系统音频录制",
      ),
      "start",
    );

    expect(result.code).toBe("SYSTEM_AUDIO_PERMISSION_DENIED");
    expect(result.title).toBe("无法录制电脑音频");
    expect(result.failedAction).toBe("start");
  });

  it("keeps realtime session preparation timeout distinct from unknown failures", () => {
    const result = toMeetingError(
      new RealtimeTranscriptionError(
        "TRANSCRIPTION_SESSION_READY_TIMEOUT",
        "实时转写服务在 45 秒内未完成会话准备，请稍后重试。",
        true,
      ),
      "start",
    );

    expect(result.code).toBe("TRANSCRIPTION_SESSION_READY_TIMEOUT");
    expect(result.title).toBe("实时转写准备超时");
    expect(result.retryable).toBe(true);
    expect(result.failedAction).toBe("start");
  });
});

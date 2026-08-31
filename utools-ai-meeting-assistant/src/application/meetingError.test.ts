import { describe, expect, it } from "vitest";

import { ApiError } from "@/infrastructure/api/apiClient";
import { AudioCaptureError } from "@/infrastructure/audio/microphoneRecorder";

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
});

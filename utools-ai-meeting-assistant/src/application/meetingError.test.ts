import { describe, expect, it } from "vitest";

import { ApiError } from "@/infrastructure/api/apiClient";
import { AudioCaptureError } from "@/infrastructure/audio/microphoneRecorder";

import { toMeetingError } from "./meetingError";

describe("meeting error mapping", () => {
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

  it("reports invalid local PCM settings without an unknown error code", () => {
    const result = toMeetingError(
      new Error("实时音频参数必须为 16kHz 单声道 PCM16LE，分片长度为 100-200ms"),
      "start",
    );

    expect(result.code).toBe("LOCAL_AUDIO_CONFIGURATION_INVALID");
    expect(result.title).toBe("本地音频配置无效");
    expect(result.retryable).toBe(false);
  });

});

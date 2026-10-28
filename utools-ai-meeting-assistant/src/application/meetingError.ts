import { ApiError, ApiRequestTimeoutError } from "@/infrastructure/api/apiClient";
import { AudioCaptureError } from "@/infrastructure/audio/microphoneRecorder";

export type FailedMeetingAction = "start" | "stop" | "export";

export type MeetingErrorCode =
  | "MICROPHONE_PERMISSION_DENIED"
  | "MICROPHONE_NOT_FOUND"
  | "SYSTEM_AUDIO_PERMISSION_DENIED"
  | "SYSTEM_AUDIO_UNAVAILABLE"
  | "UTOOLS_AUTH_NOT_CONFIGURED"
  | "NETWORK_UNAVAILABLE"
  | "LOCAL_MODEL_DOWNLOAD_FAILED"
  | "LOCAL_AUDIO_CONFIGURATION_INVALID"
  | "LOCAL_MEETING_FINALIZATION_FAILED"
  | "EXPORT_FAILED"
  | "UNKNOWN_ERROR";

export interface MeetingErrorInfo {
  code: MeetingErrorCode;
  title: string;
  message: string;
  retryable: boolean;
  failedAction?: FailedMeetingAction;
}

export function toMeetingError(
  error: unknown,
  failedAction?: FailedMeetingAction,
): MeetingErrorInfo {
  if (error instanceof AudioCaptureError) {
    return {
      code: error.code,
      title:
        error.code === "SYSTEM_AUDIO_PERMISSION_DENIED"
          ? "无法录制电脑音频"
          : "系统音频不可用",
      message: error.message,
      retryable: true,
      failedAction,
    };
  }
  if (isMicrophonePermissionError(error)) {
    return {
      code: "MICROPHONE_PERMISSION_DENIED",
      title: "无法使用麦克风",
      message: microphonePermissionMessage(),
      retryable: true,
      failedAction,
    };
  }
  if (isMissingMicrophoneError(error)) {
    return {
      code: "MICROPHONE_NOT_FOUND",
      title: "没有找到麦克风",
      message: "请连接或启用麦克风设备，然后重试。",
      retryable: true,
      failedAction,
    };
  }
  if (error instanceof Error && error.message.includes("实时音频参数")) {
    return {
      code: "LOCAL_AUDIO_CONFIGURATION_INVALID",
      title: "本地音频配置无效",
      message: error.message,
      retryable: false,
    };
  }
  if (error instanceof ApiError) {
    return mapApiError(error, failedAction);
  }
  if (error instanceof ApiRequestTimeoutError) {
    return {
      code: "NETWORK_UNAVAILABLE",
      title: "会议服务响应超时",
      message: `${error.message}，请检查网络后重试。`,
      retryable: true,
      failedAction,
    };
  }
  if (error instanceof TypeError && /fetch|network|load/i.test(error.message)) {
    return {
      code: "NETWORK_UNAVAILABLE",
      title: "网络连接失败",
      message: "无法连接会议服务，请检查网络后重试。",
      retryable: true,
      failedAction,
    };
  }
  if (failedAction === "export") {
    return {
      code: "EXPORT_FAILED",
      title: "导出失败",
      message: error instanceof Error ? error.message : "无法保存 Markdown 文件。",
      retryable: true,
      failedAction,
    };
  }
  return {
    code: "UNKNOWN_ERROR",
    title: "操作失败",
    message: error instanceof Error ? error.message : "发生未知错误，请重试。",
    retryable: true,
    failedAction,
  };
}

function microphonePermissionMessage(): string {
  const platform = window.meetingDesktop?.getRuntimeInfo().platform;
  if (platform === "win32") {
    return "请在 Windows 设置的隐私和安全性中允许桌面应用使用麦克风，然后重试。";
  }
  return "请在 macOS 系统设置中允许 uTools 使用麦克风，然后重试。";
}

function mapApiError(error: ApiError, failedAction?: FailedMeetingAction): MeetingErrorInfo {
  switch (error.reason) {
    case "UTOOLS_AUTH_NOT_CONFIGURED":
      return {
        code: "UTOOLS_AUTH_NOT_CONFIGURED",
        title: "uTools 登录尚未配置",
        message: "会议服务缺少当前插件的服务端身份配置，请联系管理员完成配置后重试。",
        retryable: false,
      };
    default:
      return {
        code: "UNKNOWN_ERROR",
        title: "登录服务返回错误",
        message: error.message,
        retryable: error.status >= 500 || error.status === 408 || error.status === 429,
        failedAction,
      };
  }
}

function isMicrophonePermissionError(error: unknown): boolean {
  return error instanceof DOMException && ["NotAllowedError", "SecurityError"].includes(error.name);
}

function isMissingMicrophoneError(error: unknown): boolean {
  return error instanceof DOMException && ["NotFoundError", "DevicesNotFoundError"].includes(error.name);
}

import { ApiError } from "@/infrastructure/api/apiClient";
import { AudioCaptureError } from "@/infrastructure/audio/microphoneRecorder";
import { RealtimeTranscriptionError } from "@/infrastructure/realtime/transcriptionClient";

export type FailedMeetingAction = "start" | "stop" | "export";

export type MeetingErrorCode =
  | "MICROPHONE_PERMISSION_DENIED"
  | "MICROPHONE_NOT_FOUND"
  | "SYSTEM_AUDIO_PERMISSION_DENIED"
  | "SYSTEM_AUDIO_UNAVAILABLE"
  | "UTOOLS_AUTH_NOT_CONFIGURED"
  | "MEETING_QUOTA_EXCEEDED"
  | "MEETING_CONCURRENT_LIMIT_REACHED"
  | "TRANSCRIPTION_SESSION_EXPIRED"
  | "TRANSCRIPTION_SESSION_READY_TIMEOUT"
  | "NETWORK_UNAVAILABLE"
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
  if (error instanceof ApiError) {
    return mapApiError(error, failedAction);
  }
  if (error instanceof RealtimeTranscriptionError) {
    return mapRealtimeError(error, failedAction);
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

function mapRealtimeError(
  error: RealtimeTranscriptionError,
  failedAction?: FailedMeetingAction,
): MeetingErrorInfo {
  switch (error.code) {
    case "TRANSCRIPTION_SESSION_READY_TIMEOUT":
      return {
        code: "TRANSCRIPTION_SESSION_READY_TIMEOUT",
        title: "实时转写准备超时",
        message: error.message,
        retryable: true,
        failedAction,
      };
    case "MEETING_QUOTA_EXCEEDED":
      return {
        code: "MEETING_QUOTA_EXCEEDED",
        title: "本月会议分钟数已用完",
        message: error.message,
        retryable: false,
      };
    case "MEETING_CONCURRENT_LIMIT_REACHED":
      return {
        code: "MEETING_CONCURRENT_LIMIT_REACHED",
        title: "已有会议尚未结束",
        message: "上一场会议正在结束或释放额度，请稍后重试。",
        retryable: true,
        failedAction,
      };
    case "TRANSCRIPTION_SESSION_EXPIRED":
    case "TRANSCRIPTION_TICKET_INVALID":
      return {
        code: "TRANSCRIPTION_SESSION_EXPIRED",
        title: "实时转写会话不可用",
        message: error.message,
        retryable: false,
      };
    default:
      return {
        code: "UNKNOWN_ERROR",
        title: "实时转写发生错误",
        message: error.message,
        retryable: error.retryable,
        failedAction,
      };
  }
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
    case "MEETING_QUOTA_EXCEEDED":
      return {
        code: "MEETING_QUOTA_EXCEEDED",
        title: "本月会议分钟数已用完",
        message: "当前账号没有可用会议时长，请稍后查看额度或升级方案。",
        retryable: false,
      };
    case "MEETING_CONCURRENT_LIMIT_REACHED":
      return {
        code: "MEETING_CONCURRENT_LIMIT_REACHED",
        title: "已有会议尚未结束",
        message: "上一场会议正在结束或释放额度，请稍后重试。",
        retryable: true,
        failedAction,
      };
    case "TRANSCRIPTION_SESSION_EXPIRED":
      return {
        code: "TRANSCRIPTION_SESSION_EXPIRED",
        title: "实时转写会话已过期",
        message: "本次实时连接无法继续，请安全停止会议后重新开始。",
        retryable: false,
      };
    default:
      return {
        code: "UNKNOWN_ERROR",
        title: "会议服务返回错误",
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

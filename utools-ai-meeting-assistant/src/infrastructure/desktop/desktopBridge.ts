const browserBridge: MeetingDesktopBridge = {
  getRuntimeInfo: () => ({
    platform: "browser",
    arch: "unknown",
    isDev: true,
    windowType: "browser",
  }),
  getLastEntryAction: () => null,
  subscribeLifecycle: () => () => undefined,
  getUToolsUser: () => null,
  getUserServerTemporaryToken: async () => ({
    token: "browser-development-token",
    expired_at: Date.now() + 5 * 60 * 1000,
  }),
  startSystemAudioCapture: async () => {
    throw new Error("系统音频采集只在 uTools 桌面端可用");
  },
  stopSystemAudioCapture: async () => undefined,
  saveMarkdown: async ({ suggestedName, content }) => {
    const blob = new Blob([content], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = suggestedName;
    anchor.click();
    URL.revokeObjectURL(url);
    return { saved: true };
  },
  getRecordingDirectory: () => "当前浏览器本地存储",
  saveRecording: async () => {
    throw new Error("浏览器环境不支持 uTools 文件录音存储");
  },
  listRecordings: async () => [],
  readRecording: async () => {
    throw new Error("浏览器环境不支持 uTools 文件录音读取");
  },
  deleteRecording: async () => {
    throw new Error("浏览器环境不支持 uTools 文件录音删除");
  },
  notify: (message) => window.alert(message),
};

export function getDesktopBridge(): MeetingDesktopBridge {
  return window.meetingDesktop ?? browserBridge;
}

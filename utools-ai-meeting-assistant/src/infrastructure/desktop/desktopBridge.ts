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
  getLocalTranscriptionStatus: async ({ modelId }) => ({
    supported: false,
    binaryAvailable: false,
    modelDownloaded: false,
    running: false,
    selectedModelId: modelId,
    modelName: "Whisper Small Q5",
    modelSizeBytes: 190_085_487,
    models: [
      { id: "tiny-q5_1", name: "Whisper Tiny Q5", description: "速度最快，适合较清晰的短会议", sizeBytes: 32_152_673, downloaded: false, recommended: false },
      { id: "base-q5_1", name: "Whisper Base Q5", description: "速度与识别效果的轻量平衡", sizeBytes: 59_707_625, downloaded: false, recommended: false },
      { id: "small-q5_1", name: "Whisper Small Q5", description: "中文会议效果更均衡，推荐使用", sizeBytes: 190_085_487, downloaded: false, recommended: true },
      { id: "medium-q5_0", name: "Whisper Medium Q5", description: "准确率更高，需要更多内存与处理时间", sizeBytes: 539_212_467, downloaded: false, recommended: false },
    ],
  }),
  downloadLocalTranscriptionModel: async () => {
    throw new Error("本地转写只在 uTools 桌面端可用");
  },
  startLocalTranscription: async () => {
    throw new Error("本地转写只在 uTools 桌面端可用");
  },
  transcribeLocalAudio: async () => {
    throw new Error("本地转写只在 uTools 桌面端可用");
  },
  stopLocalTranscription: async () => undefined,
  listOllamaModels: async () => {
    throw new Error("本地 Ollama 只在 uTools 桌面端可用");
  },
  chatOllama: async () => {
    throw new Error("本地 Ollama 只在 uTools 桌面端可用");
  },
  chatOpenAICompatible: async () => {
    throw new Error("通用模型总结只在 uTools 桌面端可用");
  },
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
  readRecordingTranscript: async () => [],
  readRecordingSummary: async () => null,
  saveRecordingSummary: async () => {
    throw new Error("浏览器环境不支持本地会议总结存储");
  },
  deleteRecording: async () => {
    throw new Error("浏览器环境不支持 uTools 文件录音删除");
  },
  notify: (message) => window.alert(message),
};

export function getDesktopBridge(): MeetingDesktopBridge {
  return window.meetingDesktop ?? browserBridge;
}

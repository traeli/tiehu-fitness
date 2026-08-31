interface UToolsUser {
  avatar: string;
  nickname: string;
  type: "member" | "user";
}

interface UToolsTemporaryToken {
  token: string;
  expired_at: number;
}

type MeetingWindowType = "main" | "detach" | "browser";

interface MeetingPluginEntryAction {
  code: string;
  type: string;
  from: string;
}

type MeetingPluginLifecycleEvent =
  | { type: "enter"; at: number; action: MeetingPluginEntryAction }
  | { type: "out"; at: number; isKill: boolean }
  | { type: "detach"; at: number };

interface MeetingDesktopBridge {
  getRuntimeInfo(): {
    platform: string;
    arch: string;
    isDev: boolean;
    windowType: MeetingWindowType;
  };
  getLastEntryAction(): MeetingPluginEntryAction | null;
  subscribeLifecycle(listener: (event: MeetingPluginLifecycleEvent) => void): () => void;
  getUToolsUser(): UToolsUser | null;
  getUserServerTemporaryToken(): Promise<UToolsTemporaryToken>;
  startSystemAudioCapture(
    options: { sampleRate: 48000; channels: 1; format: "pcm_s16le" },
    onAudio: (chunk: ArrayBuffer) => void,
    onError: (failure: { code: string; message: string }) => void,
  ): Promise<{ sampleRate: 48000; channels: 1; format: "pcm_s16le" }>;
  stopSystemAudioCapture(): Promise<void>;
  saveMarkdown(input: {
    suggestedName: string;
    content: string;
  }): Promise<{ saved: boolean; path?: string }>;
  getRecordingDirectory(): string;
  saveRecording(input: {
    id: string;
    meetingId: string;
    createdAt: string;
    durationMs: number;
    mimeType: string;
    audioData: ArrayBuffer;
  }): Promise<void>;
  listRecordings(): Promise<MeetingDesktopRecording[]>;
  readRecording(id: string): Promise<{ mimeType: string; audioData: ArrayBuffer }>;
  deleteRecording(id: string): Promise<void>;
  notify(message: string): void;
}

interface MeetingDesktopRecording {
  id: string;
  meetingId: string;
  createdAt: string;
  durationMs: number;
  mimeType: string;
  sizeBytes: number;
}

interface Window {
  meetingDesktop?: MeetingDesktopBridge;
}

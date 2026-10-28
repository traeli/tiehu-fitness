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
  getLocalTranscriptionStatus(input: { modelId: LocalTranscriptionModelID }): Promise<LocalTranscriptionStatus>;
  downloadLocalTranscriptionModel(
    input: { modelId: LocalTranscriptionModelID },
    onProgress: (progress: LocalTranscriptionProgress) => void,
  ): Promise<LocalTranscriptionStatus>;
  startLocalTranscription(input: { modelId: LocalTranscriptionModelID }): Promise<void>;
  transcribeLocalAudio(input: { pcmData: ArrayBuffer }): Promise<LocalTranscriptionSegment[]>;
  stopLocalTranscription(): Promise<void>;
  listOllamaModels(baseURL: string): Promise<string[]>;
  chatOllama(input: {
    baseURL: string;
    model: string;
    systemPrompt: string;
    userPrompt: string;
  }): Promise<{ content: string; inputTokens: number; outputTokens: number }>;
  chatOpenAICompatible(input: {
    provider: "openai" | "deepseek";
    baseURL: string;
    apiKey: string;
    model: string;
    systemPrompt: string;
    userPrompt: string;
  }): Promise<{ content: string; inputTokens: number; outputTokens: number }>;
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
    transcript: MeetingTranscriptSegment[];
  }): Promise<void>;
  listRecordings(): Promise<MeetingDesktopRecording[]>;
  readRecording(id: string): Promise<{ mimeType: string; audioData: ArrayBuffer }>;
  readRecordingTranscript(id: string): Promise<MeetingTranscriptSegment[]>;
  readRecordingSummary(id: string): Promise<MeetingLocalSummary | null>;
  saveRecordingSummary(id: string, summary: MeetingLocalSummary): Promise<void>;
  deleteRecording(id: string): Promise<void>;
  notify(message: string): void;
}

interface LocalTranscriptionStatus {
  supported: boolean;
  binaryAvailable: boolean;
  modelDownloaded: boolean;
  running: boolean;
  selectedModelId: LocalTranscriptionModelID;
  modelName: string;
  modelSizeBytes: number;
  models: LocalTranscriptionModelStatus[];
}

type LocalTranscriptionModelID = "tiny-q5_1" | "base-q5_1" | "small-q5_1" | "medium-q5_0";

interface LocalTranscriptionModelStatus {
  id: LocalTranscriptionModelID;
  name: string;
  description: string;
  sizeBytes: number;
  downloaded: boolean;
  recommended: boolean;
}

type LocalTranscriptionProgress =
  | {
      phase: "downloading";
      receivedBytes: number;
      totalBytes: number;
    }
  | {
      phase: "verifying";
      receivedBytes: number;
      totalBytes: number;
    };

interface LocalTranscriptionSegment {
  startMs: number;
  endMs: number;
  text: string;
}

interface MeetingTranscriptSegment {
  id: string;
  sequenceNo: number;
  startOffsetMs: number;
  endOffsetMs: number;
  speakerLabel?: string;
  content: string;
  isFinal: boolean;
}

interface MeetingLocalSummary {
  topic: string;
  abstract: string;
  keyDiscussions: string[];
  decisions: string[];
  actionItems: Array<{ assignee: string; task: string; dueText: string; status: "pending" }>;
  risks: string[];
  provider: "ollama" | "openai" | "deepseek";
  model: string;
  promptVersion: "meeting-summary-v2-local";
  generatedAt: string;
  inputTokens: number;
  outputTokens: number;
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

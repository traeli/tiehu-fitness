const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { NativeSystemAudioProcess } = require("./native-audio-process.cjs");
const { LocalTranscriptionProcess } = require("./local-transcription-process.cjs");

const lifecycleListeners = new Set();
let lastEntryAction = null;
let recordingMutationQueue = Promise.resolve();
const nativeSystemAudio = new NativeSystemAudioProcess({
  runtimeDirectory:
    typeof utools === "undefined"
      ? undefined
      : path.join(utools.getPath("userData"), "meeting-assistant", "native"),
});
const localTranscription = new LocalTranscriptionProcess({
  runtimeDirectory: path.join(__dirname, "native", `${process.platform}-${process.arch}`),
  modelDirectory:
    typeof utools === "undefined"
      ? path.join(os.tmpdir(), "meeting-assistant-models")
      : path.join(utools.getPath("userData"), "meeting-assistant", "models"),
});
const recordingDirectoryName = "会议助手";
const legacyRecordingDirectoryName = "铁虎AI会议助手";
const recordingSubdirectoryName = "录音";
const recordingIndexFileName = "index.json";
const maxRecordingBytes = 256 * 1024 * 1024;
const maxRecordingCount = 1_000;
const maxIndexBytes = 1024 * 1024;
const recordingIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const recordingExtensions = new Map([
  ["audio/webm;codecs=opus", "webm"],
  ["audio/webm", "webm"],
  ["audio/mp4", "m4a"],
]);

const emitLifecycle = (event) => {
  for (const listener of lifecycleListeners) {
    try {
      listener(event);
    } catch (error) {
      console.error("meeting lifecycle listener failed", error);
    }
  }
};

const sanitizeEntryAction = (action) => ({
  code: typeof action?.code === "string" ? action.code : "",
  type: typeof action?.type === "string" ? action.type : "",
  from: typeof action?.from === "string" ? action.from : "",
});

if (typeof utools !== "undefined") {
  utools.onPluginEnter((action) => {
    lastEntryAction = sanitizeEntryAction(action);
    emitLifecycle({ type: "enter", at: Date.now(), action: lastEntryAction });
  });
  utools.onPluginOut((isKill) => {
    localTranscription.stop();
    void nativeSystemAudio.stop().catch((error) => {
      console.error("stop native system audio after plugin exit failed", error);
    });
    emitLifecycle({ type: "out", at: Date.now(), isKill: Boolean(isKill) });
  });
  utools.onPluginDetach(() => {
    localTranscription.stop();
    void nativeSystemAudio.stop().catch((error) => {
      console.error("stop native system audio after plugin detach failed", error);
    });
    emitLifecycle({ type: "detach", at: Date.now() });
  });
}

const safeFileName = (value) => {
  const normalized = String(value || "meeting-summary.md")
    .replace(/[\\/:*?"<>|]/g, "-")
    .trim();
  return normalized.endsWith(".md") ? normalized : `${normalized}.md`;
};

const requireUTools = () => {
  if (typeof utools === "undefined") {
    throw new Error("uTools runtime is unavailable");
  }
};

const recordingDirectory = () => {
  requireUTools();
  return path.join(
    utools.getPath("documents"),
    recordingDirectoryName,
    recordingSubdirectoryName,
  );
};

const legacyRecordingDirectory = () => {
  requireUTools();
  return path.join(
    utools.getPath("documents"),
    legacyRecordingDirectoryName,
    recordingSubdirectoryName,
  );
};

let recordingDirectoryInitialization;

const ensureRecordingDirectory = async () => {
  if (recordingDirectoryInitialization) {
    return recordingDirectoryInitialization;
  }
  recordingDirectoryInitialization = (async () => {
    const directory = recordingDirectory();
    try {
      const stat = await fs.stat(directory);
      if (!stat.isDirectory()) {
        throw new Error("Recording directory path is not a directory");
      }
      return directory;
    } catch (error) {
      if (!error || error.code !== "ENOENT") {
        throw error;
      }
    }

    const legacyDirectory = legacyRecordingDirectory();
    try {
      const legacyStat = await fs.stat(legacyDirectory);
      if (!legacyStat.isDirectory()) {
        throw new Error("Legacy recording directory path is not a directory");
      }
      await fs.mkdir(path.dirname(directory), { recursive: true });
      await fs.rename(legacyDirectory, directory);
      try {
        await fs.rmdir(path.dirname(legacyDirectory));
      } catch (cleanupError) {
        if (!cleanupError || !["ENOENT", "ENOTEMPTY", "EEXIST"].includes(cleanupError.code)) {
          console.error("cleanup legacy recording directory failed", cleanupError);
        }
      }
      return directory;
    } catch (error) {
      if (!error || error.code !== "ENOENT") {
        throw error;
      }
    }

    await fs.mkdir(directory, { recursive: true });
    return directory;
  })();
  return recordingDirectoryInitialization;
};

const recordingIndexPath = () => path.join(recordingDirectory(), recordingIndexFileName);

const validateRecordingID = (value, field) => {
  if (typeof value !== "string" || !recordingIDPattern.test(value)) {
    throw new TypeError(`${field} must be a UUID`);
  }
  return value;
};

const validateRecordingMetadata = (value) => {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError("Recording metadata must be an object");
  }
  const id = validateRecordingID(value.id, "Recording ID");
  const meetingId = validateRecordingID(value.meetingId, "Meeting ID");
  if (typeof value.createdAt !== "string" || Number.isNaN(Date.parse(value.createdAt))) {
    throw new TypeError("Recording creation time is invalid");
  }
  if (!Number.isSafeInteger(value.durationMs) || value.durationMs <= 0) {
    throw new TypeError("Recording duration is invalid");
  }
  if (!recordingExtensions.has(value.mimeType)) {
    throw new TypeError("Recording MIME type is unsupported");
  }
  if (!Number.isSafeInteger(value.sizeBytes) || value.sizeBytes <= 0 || value.sizeBytes > maxRecordingBytes) {
    throw new TypeError("Recording size is invalid");
  }
  const expectedFileName = `${id}.${recordingExtensions.get(value.mimeType)}`;
  if (value.fileName !== expectedFileName) {
    throw new TypeError("Recording file name is invalid");
  }
  return {
    id,
    meetingId,
    createdAt: new Date(value.createdAt).toISOString(),
    durationMs: value.durationMs,
    mimeType: value.mimeType,
    sizeBytes: value.sizeBytes,
    fileName: expectedFileName,
  };
};

const readRecordingIndex = async () => {
  await ensureRecordingDirectory();
  const indexPath = recordingIndexPath();
  try {
    const stat = await fs.stat(indexPath);
    if (!stat.isFile() || stat.size > maxIndexBytes) {
      throw new Error("Recording index is invalid or too large");
    }
    const value = JSON.parse(await fs.readFile(indexPath, "utf8"));
    if (!Array.isArray(value) || value.length > maxRecordingCount) {
      throw new Error("Recording index contains too many entries");
    }
    return value.map(validateRecordingMetadata);
  } catch (error) {
    if (error && error.code === "ENOENT") {
      return [];
    }
    throw error;
  }
};

const writeRecordingIndex = async (recordings) => {
  const directory = await ensureRecordingDirectory();
  const indexPath = recordingIndexPath();
  const temporaryPath = path.join(
    directory,
    `${recordingIndexFileName}.${process.pid}.${Date.now()}.tmp`,
  );
  try {
    await fs.writeFile(temporaryPath, JSON.stringify(recordings, null, 2), {
      encoding: "utf8",
      flag: "wx",
    });
    await fs.rename(temporaryPath, indexPath);
  } catch (error) {
    try {
      await fs.unlink(temporaryPath);
    } catch (cleanupError) {
      if (!cleanupError || cleanupError.code !== "ENOENT") {
        console.error("cleanup recording index temporary file failed", cleanupError);
      }
    }
    throw error;
  }
};

const publicRecordingMetadata = ({ fileName: _fileName, ...metadata }) => metadata;

const validateTranscript = (value) => {
  if (!Array.isArray(value) || value.length > 100_000) {
    throw new TypeError("Recording transcript is invalid");
  }
  return value.map((segment, index) => {
    if (
      !segment || typeof segment !== "object" ||
      typeof segment.id !== "string" || segment.id.length > 100 ||
      !Number.isSafeInteger(segment.sequenceNo) || segment.sequenceNo < 0 ||
      !Number.isSafeInteger(segment.startOffsetMs) || segment.startOffsetMs < 0 ||
      !Number.isSafeInteger(segment.endOffsetMs) || segment.endOffsetMs < segment.startOffsetMs ||
      typeof segment.content !== "string" || segment.content.length > 20_000
    ) {
      throw new TypeError(`Recording transcript segment ${index} is invalid`);
    }
    return {
      id: segment.id,
      sequenceNo: segment.sequenceNo,
      startOffsetMs: segment.startOffsetMs,
      endOffsetMs: segment.endOffsetMs,
      speakerLabel: typeof segment.speakerLabel === "string" ? segment.speakerLabel.slice(0, 100) : undefined,
      content: segment.content,
      isFinal: true,
    };
  });
};

const transcriptPath = (id) => path.join(recordingDirectory(), `${id}.transcript.json`);
const summaryPath = (id) => path.join(recordingDirectory(), `${id}.summary.json`);

const meetingSummarySchema = Object.freeze({
  type: "object",
  properties: {
    topic: { type: "string" },
    abstract: { type: "string" },
    key_discussions: { type: "array", items: { type: "string" } },
    decisions: { type: "array", items: { type: "string" } },
    action_items: {
      type: "array",
      items: {
        type: "object",
        additionalProperties: false,
        properties: {
          assignee: { type: "string" },
          task: { type: "string" },
          due_text: { type: "string" },
          status: { type: "string", enum: ["pending"] },
        },
        required: ["assignee", "task", "due_text", "status"],
      },
    },
    risks: { type: "array", items: { type: "string" } },
  },
  additionalProperties: false,
  required: ["topic", "abstract", "key_discussions", "decisions", "action_items", "risks"],
});

const enqueueRecordingMutation = (operation) => {
  const result = recordingMutationQueue.then(operation, operation);
  recordingMutationQueue = result.catch(() => undefined);
  return result;
};

window.meetingDesktop = Object.freeze({
  getRuntimeInfo() {
    return {
      platform: os.platform(),
      arch: os.arch(),
      isDev: typeof utools !== "undefined" && utools.isDev(),
      windowType:
        typeof utools === "undefined" ? "browser" : utools.getWindowType(),
    };
  },

  getLastEntryAction() {
    return lastEntryAction;
  },

  subscribeLifecycle(listener) {
    if (typeof listener !== "function") {
      throw new TypeError("Lifecycle listener must be a function");
    }
    lifecycleListeners.add(listener);
    return () => lifecycleListeners.delete(listener);
  },

  getUToolsUser() {
    return typeof utools === "undefined" ? null : utools.getUser();
  },

  async getUserServerTemporaryToken() {
    if (typeof utools === "undefined") {
      throw new Error("uTools runtime is unavailable");
    }
    return utools.fetchUserServerTemporaryToken();
  },

  async startSystemAudioCapture(options, onAudio, onError) {
    requireUTools();
    if (
      !options ||
      options.sampleRate !== 48_000 ||
      options.channels !== 1 ||
      options.format !== "pcm_s16le"
    ) {
      throw new TypeError("Native system audio options are invalid");
    }
    if (typeof onAudio !== "function" || typeof onError !== "function") {
      throw new TypeError("Native system audio callbacks must be functions");
    }
    return nativeSystemAudio.start({
      onAudio,
      onError: (error) => onError({
        code: typeof error?.code === "string" ? error.code : "SYSTEM_AUDIO_NATIVE_FAILED",
        message: error instanceof Error ? error.message : "系统音频采集失败",
      }),
    });
  },

  async stopSystemAudioCapture() {
    await nativeSystemAudio.stop();
  },

  async getLocalTranscriptionStatus(input) {
    requireUTools();
    return localTranscription.status(input?.modelId);
  },

  async downloadLocalTranscriptionModel(input, onProgress) {
    requireUTools();
    if (onProgress !== undefined && typeof onProgress !== "function") {
      throw new TypeError("Model download progress callback must be a function");
    }
    return localTranscription.downloadModel(input?.modelId, onProgress);
  },

  async startLocalTranscription(input) {
    requireUTools();
    await localTranscription.start(input?.modelId);
  },

  async transcribeLocalAudio(input) {
    requireUTools();
    return localTranscription.transcribe(input?.pcmData);
  },

  async stopLocalTranscription() {
    localTranscription.stop();
  },

  async listOllamaModels(baseURL) {
    requireUTools();
    const origin = validateOllamaBaseURL(baseURL);
    const response = await fetchJSON(`${origin}/api/tags`, { method: "GET" }, 10_000, "Ollama");
    if (!Array.isArray(response?.models)) {
      throw new Error("Ollama 模型列表响应格式无效");
    }
    return response.models
      .map((model) => typeof model?.name === "string" ? model.name.trim() : "")
      .filter(Boolean)
      .slice(0, 200);
  },

  async chatOllama(input) {
    requireUTools();
    const origin = validateOllamaBaseURL(input?.baseURL);
    const model = requireBoundedString(input?.model, "Ollama model", 200);
    const systemPrompt = requireBoundedString(input?.systemPrompt, "Ollama system prompt", 20_000);
    const userPrompt = requireBoundedString(input?.userPrompt, "Ollama user prompt", 400_000);
    const response = await fetchJSON(`${origin}/api/chat`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model,
        messages: [
          { role: "system", content: systemPrompt },
          { role: "user", content: userPrompt },
        ],
        stream: false,
        think: false,
        format: meetingSummarySchema,
        options: { temperature: 0, num_predict: 2_048 },
        keep_alive: "10m",
      }),
    }, 5 * 60_000, "Ollama");
    const content = response?.message?.content;
    if (typeof content !== "string" || !content.trim()) {
      throw new Error("Ollama 没有返回可用的会议总结");
    }
    return {
      content,
      inputTokens: Number.isSafeInteger(response.prompt_eval_count) ? response.prompt_eval_count : 0,
      outputTokens: Number.isSafeInteger(response.eval_count) ? response.eval_count : 0,
    };
  },

  async chatOpenAICompatible(input) {
    requireUTools();
    const provider = input?.provider === "openai" || input?.provider === "deepseek"
      ? input.provider
      : undefined;
    if (!provider) throw new TypeError("AI 总结服务类型无效");
    const serviceName = provider === "openai" ? "OpenAI" : "DeepSeek";
    const baseURL = validateCompatibleBaseURL(input?.baseURL);
    const apiKey = typeof input?.apiKey === "string" && input.apiKey.length <= 500
      ? input.apiKey.trim()
      : "";
    const model = requireBoundedString(input?.model, "Compatible model", 200);
    const systemPrompt = requireBoundedString(input?.systemPrompt, "Compatible system prompt", 20_000);
    const userPrompt = requireBoundedString(input?.userPrompt, "Compatible user prompt", 400_000);
    const response = await fetchJSON(`${baseURL}/chat/completions`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(apiKey ? { Authorization: `Bearer ${apiKey}` } : {}),
      },
      body: JSON.stringify({
        model,
        messages: [
          { role: "system", content: systemPrompt },
          { role: "user", content: userPrompt },
        ],
        stream: false,
        response_format: { type: "json_object" },
        max_tokens: 2_048,
        temperature: 0,
      }),
    }, 5 * 60_000, serviceName);
    const content = response?.choices?.[0]?.message?.content;
    if (typeof content !== "string" || !content.trim()) {
      throw new Error(`${serviceName} 没有返回可用的会议总结`);
    }
    return {
      content,
      inputTokens: Number.isSafeInteger(response?.usage?.prompt_tokens) ? response.usage.prompt_tokens : 0,
      outputTokens: Number.isSafeInteger(response?.usage?.completion_tokens) ? response.usage.completion_tokens : 0,
    };
  },

  async saveMarkdown({ suggestedName, content }) {
    if (typeof utools === "undefined") {
      throw new Error("uTools runtime is unavailable");
    }
    if (typeof content !== "string") {
      throw new TypeError("Markdown content must be a string");
    }

    const fileName = safeFileName(suggestedName);
    const selectedPath = utools.showSaveDialog({
      defaultPath: path.join(utools.getPath("documents"), fileName),
      filters: [{ name: "Markdown", extensions: ["md"] }],
    });
    if (!selectedPath) {
      return { saved: false };
    }

    await fs.writeFile(selectedPath, content, { encoding: "utf8", flag: "w" });
    return { saved: true, path: selectedPath };
  },

  getRecordingDirectory() {
    return recordingDirectory();
  },

  async saveRecording(input) {
    requireUTools();
    if (!(input?.audioData instanceof ArrayBuffer)) {
      throw new TypeError("Recording audio data must be an ArrayBuffer");
    }
    if (input.audioData.byteLength <= 0 || input.audioData.byteLength > maxRecordingBytes) {
      throw new TypeError("Recording audio size is invalid");
    }
    const extension = recordingExtensions.get(input.mimeType);
    if (!extension) {
      throw new TypeError("Recording MIME type is unsupported");
    }
    const metadata = validateRecordingMetadata({
      id: input.id,
      meetingId: input.meetingId,
      createdAt: input.createdAt,
      durationMs: input.durationMs,
      mimeType: input.mimeType,
      sizeBytes: input.audioData.byteLength,
      fileName: `${input.id}.${extension}`,
    });
    const transcript = validateTranscript(input.transcript);
    return enqueueRecordingMutation(async () => {
      const directory = await ensureRecordingDirectory();
      const recordings = await readRecordingIndex();
      if (recordings.some((recording) => recording.id === metadata.id)) {
        throw new Error("Recording already exists");
      }
      if (recordings.length >= maxRecordingCount) {
        throw new Error("本地录音数量已达 1000 条上限，请先删除旧录音");
      }
      const filePath = path.join(directory, metadata.fileName);
      const meetingTranscriptPath = transcriptPath(metadata.id);
      let recordingFileCreated = false;
      let transcriptFileCreated = false;
      try {
        await fs.writeFile(filePath, new Uint8Array(input.audioData), { flag: "wx" });
        recordingFileCreated = true;
        await fs.writeFile(meetingTranscriptPath, JSON.stringify(transcript, null, 2), { encoding: "utf8", flag: "wx" });
        transcriptFileCreated = true;
        await writeRecordingIndex([metadata, ...recordings]);
      } catch (error) {
        if (recordingFileCreated) {
          try {
            await fs.unlink(filePath);
          } catch (cleanupError) {
            if (!cleanupError || cleanupError.code !== "ENOENT") {
              console.error("cleanup incomplete recording file failed", cleanupError);
            }
          }
        }
        if (transcriptFileCreated) {
          await fs.unlink(meetingTranscriptPath).catch(() => undefined);
        }
        throw error;
      }
    });
  },

  async listRecordings() {
    const recordings = await readRecordingIndex();
    return recordings
      .slice()
      .sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt))
      .slice(0, 100)
      .map(publicRecordingMetadata);
  },

  async readRecording(id) {
    validateRecordingID(id, "Recording ID");
    const recordings = await readRecordingIndex();
    const metadata = recordings.find((recording) => recording.id === id);
    if (!metadata) {
      throw new Error("本地录音不存在");
    }
    const filePath = path.join(recordingDirectory(), metadata.fileName);
    const stat = await fs.stat(filePath);
    if (!stat.isFile() || stat.size !== metadata.sizeBytes || stat.size > maxRecordingBytes) {
      throw new Error("本地录音文件大小与索引不一致");
    }
    const audio = await fs.readFile(filePath);
    const audioData = Uint8Array.from(audio).buffer;
    return { mimeType: metadata.mimeType, audioData };
  },

  async readRecordingTranscript(id) {
    validateRecordingID(id, "Recording ID");
    const recordings = await readRecordingIndex();
    if (!recordings.some((recording) => recording.id === id)) {
      throw new Error("本地录音不存在");
    }
    try {
      const value = JSON.parse(await fs.readFile(transcriptPath(id), "utf8"));
      return validateTranscript(value);
    } catch (error) {
      if (error?.code === "ENOENT") {
        return [];
      }
      throw error;
    }
  },

  async readRecordingSummary(id) {
    validateRecordingID(id, "Recording ID");
    const recordings = await readRecordingIndex();
    if (!recordings.some((recording) => recording.id === id)) {
      throw new Error("本地录音不存在");
    }
    try {
      return validateMeetingSummary(JSON.parse(await fs.readFile(summaryPath(id), "utf8")));
    } catch (error) {
      if (error?.code === "ENOENT") return null;
      throw error;
    }
  },

  async saveRecordingSummary(id, summary) {
    validateRecordingID(id, "Recording ID");
    const recordings = await readRecordingIndex();
    if (!recordings.some((recording) => recording.id === id)) {
      throw new Error("本地录音不存在");
    }
    const validated = validateMeetingSummary(summary);
    await fs.writeFile(summaryPath(id), JSON.stringify(validated, null, 2), {
      encoding: "utf8",
      flag: "w",
    });
  },

  async deleteRecording(id) {
    validateRecordingID(id, "Recording ID");
    return enqueueRecordingMutation(async () => {
      const recordings = await readRecordingIndex();
      const metadata = recordings.find((recording) => recording.id === id);
      if (!metadata) {
        return;
      }
      await fs.unlink(path.join(recordingDirectory(), metadata.fileName)).catch((error) => {
        if (!error || error.code !== "ENOENT") {
          throw error;
        }
      });
      await fs.unlink(transcriptPath(id)).catch((error) => {
        if (!error || error.code !== "ENOENT") {
          throw error;
        }
      });
      await fs.unlink(summaryPath(id)).catch((error) => {
        if (!error || error.code !== "ENOENT") throw error;
      });
      await writeRecordingIndex(recordings.filter((recording) => recording.id !== id));
    });
  },

  notify(message) {
    if (typeof utools !== "undefined") {
      utools.showNotification(String(message), "meeting-assistant");
    }
  },
});

function validateOllamaBaseURL(raw) {
  let parsed;
  try {
    parsed = new URL(String(raw || "http://127.0.0.1:11434"));
  } catch {
    throw new TypeError("Ollama 地址无效");
  }
  const hostname = parsed.hostname.toLowerCase();
  if (
    parsed.protocol !== "http:" || parsed.username || parsed.password ||
    !new Set(["127.0.0.1", "localhost", "[::1]", "::1"]).has(hostname) ||
    (parsed.pathname !== "/" && parsed.pathname !== "") || parsed.search || parsed.hash
  ) {
    throw new TypeError("Ollama 只允许使用本机 HTTP 地址");
  }
  return parsed.origin;
}

function validateCompatibleBaseURL(raw) {
  let parsed;
  try {
    parsed = new URL(String(raw || ""));
  } catch {
    throw new TypeError("通用模型接口地址无效");
  }
  const hostname = parsed.hostname.toLowerCase();
  const localHost = new Set(["127.0.0.1", "localhost", "[::1]", "::1"]).has(hostname);
  if (
    !new Set(["http:", "https:"]).has(parsed.protocol) || parsed.username || parsed.password ||
    (parsed.protocol === "http:" && !localHost) || parsed.search || parsed.hash
  ) {
    throw new TypeError("通用模型接口须使用 HTTPS；本机地址可使用 HTTP");
  }
  const pathname = parsed.pathname.replace(/\/+$/, "");
  return `${parsed.origin}${pathname}`;
}

async function fetchJSON(url, init, timeoutMs, serviceName) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(url, { ...init, signal: controller.signal });
    const text = await response.text();
    if (!response.ok) {
      let detail = "";
      try {
        const decoded = JSON.parse(text);
        detail = typeof decoded?.error === "string" ? `：${decoded.error.slice(0, 500)}` : "";
      } catch {
        detail = "";
      }
      throw new Error(`${serviceName} 返回 HTTP ${response.status}${detail}`);
    }
    try {
      return JSON.parse(text);
    } catch {
      throw new Error(`${serviceName} 返回的 JSON 无效`);
    }
  } catch (error) {
    if (error?.name === "AbortError") throw new Error(`${serviceName} 请求超时`);
    if (error instanceof TypeError) {
      throw new Error(serviceName === "Ollama" ? "无法连接本机 Ollama，请确认 Ollama 已启动" : `无法连接${serviceName}，请检查地址和网络后重试`);
    }
    throw error;
  } finally {
    clearTimeout(timer);
  }
}

function requireBoundedString(value, field, maximum) {
  if (typeof value !== "string" || !value.trim() || value.length > maximum) {
    throw new TypeError(`${field} is invalid`);
  }
  return value.trim();
}

function validateMeetingSummary(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new TypeError("Meeting summary is invalid");
  const stringArray = (items, field) => {
    if (!Array.isArray(items) || !items.every((item) => typeof item === "string" && item.length <= 20_000)) {
      throw new TypeError(`Meeting summary ${field} is invalid`);
    }
    return items;
  };
  if (
    typeof value.topic !== "string" || !value.topic.trim() || value.topic.length > 2_000 ||
    typeof value.abstract !== "string" || !value.abstract.trim() || value.abstract.length > 20_000 ||
    !Array.isArray(value.actionItems) || value.actionItems.length > 1_000 ||
    typeof value.model !== "string" || !value.model.trim() ||
    value.promptVersion !== "meeting-summary-v2-local" ||
    typeof value.generatedAt !== "string" || Number.isNaN(Date.parse(value.generatedAt))
  ) throw new TypeError("Meeting summary fields are invalid");
  const actionItems = value.actionItems.map((item) => {
    if (!item || typeof item !== "object" || typeof item.task !== "string" || !item.task.trim()) {
      throw new TypeError("Meeting summary action item is invalid");
    }
    return {
      assignee: typeof item.assignee === "string" ? item.assignee : "",
      task: item.task,
      dueText: typeof item.dueText === "string" ? item.dueText : "",
      status: "pending",
    };
  });
  return {
    topic: value.topic,
    abstract: value.abstract,
    keyDiscussions: stringArray(value.keyDiscussions, "keyDiscussions"),
    decisions: stringArray(value.decisions, "decisions"),
    actionItems,
    risks: stringArray(value.risks, "risks"),
    provider: value.provider === "openai" || value.provider === "deepseek"
      ? value.provider
      : value.provider === "openai-compatible"
        ? "deepseek"
        : "ollama",
    model: value.model,
    promptVersion: value.promptVersion,
    generatedAt: new Date(value.generatedAt).toISOString(),
    inputTokens: Number.isSafeInteger(value.inputTokens) && value.inputTokens >= 0 ? value.inputTokens : 0,
    outputTokens: Number.isSafeInteger(value.outputTokens) && value.outputTokens >= 0 ? value.outputTokens : 0,
  };
}

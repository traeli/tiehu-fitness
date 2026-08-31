const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { NativeSystemAudioProcess } = require("./native-audio-process.cjs");

const lifecycleListeners = new Set();
let lastEntryAction = null;
let recordingMutationQueue = Promise.resolve();
const nativeSystemAudio = new NativeSystemAudioProcess();
const recordingDirectoryName = "铁虎AI会议助手";
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
    emitLifecycle({ type: "out", at: Date.now(), isKill: Boolean(isKill) });
  });
  utools.onPluginDetach(() => {
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
  const directory = recordingDirectory();
  await fs.mkdir(directory, { recursive: true });
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
    return enqueueRecordingMutation(async () => {
      const directory = recordingDirectory();
      await fs.mkdir(directory, { recursive: true });
      const recordings = await readRecordingIndex();
      if (recordings.some((recording) => recording.id === metadata.id)) {
        throw new Error("Recording already exists");
      }
      if (recordings.length >= maxRecordingCount) {
        throw new Error("本地录音数量已达 1000 条上限，请先删除旧录音");
      }
      const filePath = path.join(directory, metadata.fileName);
      let recordingFileCreated = false;
      try {
        await fs.writeFile(filePath, new Uint8Array(input.audioData), { flag: "wx" });
        recordingFileCreated = true;
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
      await writeRecordingIndex(recordings.filter((recording) => recording.id !== id));
    });
  },

  notify(message) {
    if (typeof utools !== "undefined") {
      utools.showNotification(String(message), "meeting-assistant");
    }
  },
});

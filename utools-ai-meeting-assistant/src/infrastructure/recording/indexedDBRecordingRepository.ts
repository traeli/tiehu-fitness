import type {
  LocalMeetingRecording,
  RecordingRepository,
  SaveLocalMeetingRecording,
} from "./recordingRepository";

const databaseName = "tiehu-ai-meeting-assistant";
const databaseVersion = 1;
const recordingStore = "recordings";
const createdAtIndex = "createdAt";
const maxListedRecordings = 100;
const maxRecordingBytes = 256 * 1024 * 1024;

interface StoredMeetingRecording extends LocalMeetingRecording {
  audio: Blob;
}

export class IndexedDBRecordingRepository implements RecordingRepository {
  async save(recording: SaveLocalMeetingRecording): Promise<void> {
    validateRecording(recording);
    const stored: StoredMeetingRecording = {
      ...recording,
      sizeBytes: recording.audio.size,
    };
    const database = await openDatabase();
    try {
      await transactionResult(database, "readwrite", (store) => store.put(stored));
    } finally {
      database.close();
    }
  }

  async list(): Promise<LocalMeetingRecording[]> {
    const database = await openDatabase();
    try {
      return await new Promise<LocalMeetingRecording[]>((resolve, reject) => {
        const transaction = database.transaction(recordingStore, "readonly");
        const index = transaction.objectStore(recordingStore).index(createdAtIndex);
        const recordings: LocalMeetingRecording[] = [];
        const request = index.openCursor(null, "prev");
        request.addEventListener("error", () => reject(request.error ?? new Error("读取本地录音失败")));
        request.addEventListener("success", () => {
          const cursor = request.result;
          if (!cursor || recordings.length >= maxListedRecordings) {
            resolve(recordings);
            return;
          }
          const value: unknown = cursor.value;
          if (!isStoredRecording(value)) {
            reject(new Error("本地录音数据格式无效"));
            return;
          }
          recordings.push(toRecordingMetadata(value));
          cursor.continue();
        });
      });
    } finally {
      database.close();
    }
  }

  async loadAudio(id: string): Promise<Blob> {
    if (!id) {
      throw new Error("录音 ID 不能为空");
    }
    const database = await openDatabase();
    try {
      const stored = await new Promise<unknown>((resolve, reject) => {
        const transaction = database.transaction(recordingStore, "readonly");
        const request = transaction.objectStore(recordingStore).get(id);
        request.addEventListener("error", () => reject(request.error ?? new Error("读取本地录音失败")));
        request.addEventListener("success", () => resolve(request.result));
      });
      if (!isStoredRecording(stored)) {
        throw new Error("本地录音不存在或数据格式无效");
      }
      return stored.audio;
    } finally {
      database.close();
    }
  }

  async delete(id: string): Promise<void> {
    if (!id) {
      throw new Error("录音 ID 不能为空");
    }
    const database = await openDatabase();
    try {
      await transactionResult(database, "readwrite", (store) => store.delete(id));
    } finally {
      database.close();
    }
  }
}

function openDatabase(): Promise<IDBDatabase> {
  if (typeof indexedDB === "undefined") {
    return Promise.reject(new Error("当前运行环境不支持本地录音存储"));
  }
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, databaseVersion);
    request.addEventListener("error", () => reject(request.error ?? new Error("打开本地录音数据库失败")));
    request.addEventListener("upgradeneeded", () => {
      const database = request.result;
      if (!database.objectStoreNames.contains(recordingStore)) {
        const store = database.createObjectStore(recordingStore, { keyPath: "id" });
        store.createIndex(createdAtIndex, "createdAt", { unique: false });
      }
    });
    request.addEventListener("success", () => resolve(request.result));
  });
}

function transactionResult(
  database: IDBDatabase,
  mode: IDBTransactionMode,
  operation: (store: IDBObjectStore) => IDBRequest,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(recordingStore, mode);
    transaction.addEventListener("complete", () => resolve());
    transaction.addEventListener("abort", () => reject(transaction.error ?? new Error("本地录音事务已取消")));
    transaction.addEventListener("error", () => reject(transaction.error ?? new Error("本地录音事务失败")));
    operation(transaction.objectStore(recordingStore));
  });
}

function validateRecording(recording: SaveLocalMeetingRecording): void {
  if (!recording.id || !recording.meetingId || Number.isNaN(Date.parse(recording.createdAt))) {
    throw new Error("本地录音标识或时间无效");
  }
  if (!Number.isSafeInteger(recording.durationMs) || recording.durationMs <= 0) {
    throw new Error("本地录音时长无效");
  }
  if (!recording.mimeType.startsWith("audio/") || recording.audio.type !== recording.mimeType) {
    throw new Error("本地录音媒体类型无效");
  }
  if (recording.audio.size <= 0 || recording.audio.size > maxRecordingBytes) {
    throw new Error("本地录音大小超出限制");
  }
}

function isStoredRecording(value: unknown): value is StoredMeetingRecording {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }
  const recording = value as Partial<StoredMeetingRecording>;
  return (
    typeof recording.id === "string" &&
    typeof recording.meetingId === "string" &&
    typeof recording.createdAt === "string" &&
    typeof recording.durationMs === "number" &&
    typeof recording.mimeType === "string" &&
    typeof recording.sizeBytes === "number" &&
    recording.audio instanceof Blob
  );
}

function toRecordingMetadata(recording: StoredMeetingRecording): LocalMeetingRecording {
  return {
    id: recording.id,
    meetingId: recording.meetingId,
    createdAt: recording.createdAt,
    durationMs: recording.durationMs,
    mimeType: recording.mimeType,
    sizeBytes: recording.sizeBytes,
  };
}

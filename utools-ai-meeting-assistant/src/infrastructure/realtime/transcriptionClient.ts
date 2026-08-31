import type { AudioConstraints, TranscriptSegment } from "@/domain/meeting";

export type RealtimeConnectionState =
  | "idle"
  | "connecting"
  | "connected"
  | "disconnected"
  | "closed";

interface TranscriptionClientOptions {
  url: string;
  sessionTicket: string;
  audio: AudioConstraints;
  onSegment: (segment: TranscriptSegment) => void;
  onError: (error: Error) => void;
  onConnectionStateChange: (state: RealtimeConnectionState) => void;
}

interface ServerMessage {
  version: number;
  type: string;
  [key: string]: unknown;
}

const protocolVersion = 1;
const pcmMIMEType = "audio/pcm;rate=16000";
const connectionTimeoutMs = 10_000;
const finishTimeoutMs = 15_000;
const heartbeatIntervalMs = 10_000;
const heartbeatTimeoutMs = 10_000;
const maxUnacknowledgedFrames = 64;
const maxSocketBufferedBytes = 512 * 1024;

export class RealtimeTranscriptionError extends Error {
  constructor(
    readonly code: string,
    message: string,
    readonly retryable: boolean,
  ) {
    super(message);
    this.name = "RealtimeTranscriptionError";
  }
}

export class TranscriptionClient {
  #socket?: WebSocket;
  #sequenceNo = 0;
  #lastACK = 0;
  #state: RealtimeConnectionState = "idle";
  #ready = false;
  #intentionalClose = false;
  #unacknowledged = new Map<number, ArrayBuffer>();
  #connectResolve?: () => void;
  #connectReject?: (error: Error) => void;
  #finishResolve?: () => void;
  #finishReject?: (error: Error) => void;
  #connectTimer?: number;
  #finishTimer?: number;
  #heartbeatTimer?: number;
  #pongTimer?: number;
  #lastPingAt?: number;
  #finishPromise?: Promise<void>;
  #backpressureReported = false;

  constructor(private readonly options: TranscriptionClientOptions) {
    validateAudioConstraints(options.audio);
  }

  connect(): Promise<void> {
    if (this.#socket) {
      return Promise.reject(new Error("实时转写连接已经创建"));
    }
    this.#setState("connecting");
    this.#intentionalClose = false;
    const socket = new WebSocket(this.options.url);
    socket.binaryType = "arraybuffer";
    this.#socket = socket;

    const promise = new Promise<void>((resolve, reject) => {
      this.#connectResolve = resolve;
      this.#connectReject = reject;
    });
    this.#connectTimer = window.setTimeout(() => {
      this.#failConnection(new Error("实时转写会话准备超时"));
    }, connectionTimeoutMs);

    socket.addEventListener("open", () => {
      socket.send(JSON.stringify({
        version: protocolVersion,
        type: "start",
        sessionTicket: this.options.sessionTicket,
        audio: this.options.audio,
      }));
    });
    socket.addEventListener("message", (event) => this.#handleMessage(event.data));
    socket.addEventListener("error", () => {
      this.#failConnection(new Error("实时转写连接发生错误"));
    });
    socket.addEventListener("close", (event) => this.#handleClose(event));
    return promise;
  }

  sendAudioChunk(chunk: ArrayBuffer, capturedAt: number): boolean {
    const socket = this.#socket;
    if (!socket || socket.readyState !== WebSocket.OPEN || !this.#ready) {
      return false;
    }
    const maxChunkBytes = expectedChunkBytes(this.options.audio);
    if (chunk.byteLength === 0 || chunk.byteLength > maxChunkBytes || chunk.byteLength % 2 !== 0) {
      this.options.onError(new Error("PCM 音频分片大小无效"));
      return false;
    }
    if (
      this.#unacknowledged.size >= maxUnacknowledgedFrames ||
      socket.bufferedAmount >= maxSocketBufferedBytes
    ) {
      if (!this.#backpressureReported) {
        this.#backpressureReported = true;
        this.options.onError(new RealtimeTranscriptionError(
          "TRANSCRIPTION_BACKPRESSURE",
          "实时转写发送队列已满，已暂停接收新的音频分片",
          true,
        ));
      }
      return false;
    }

    const sequenceNo = this.#sequenceNo + 1;
    const header = new TextEncoder().encode(`${JSON.stringify({
      version: protocolVersion,
      type: "audio_chunk",
      sequenceNo,
      capturedAt,
      mimeType: pcmMIMEType,
    })}\n`);
    const frame = new Uint8Array(header.byteLength + chunk.byteLength);
    frame.set(header, 0);
    frame.set(new Uint8Array(chunk), header.byteLength);
    socket.send(frame.buffer);
    this.#sequenceNo = sequenceNo;
    this.#unacknowledged.set(sequenceNo, chunk);
    return true;
  }

  finish(): Promise<void> {
    if (this.#finishPromise) {
      return this.#finishPromise;
    }
    const socket = this.#socket;
    if (!socket || socket.readyState !== WebSocket.OPEN || !this.#ready) {
      this.close();
      return Promise.resolve();
    }
    this.#finishPromise = new Promise<void>((resolve, reject) => {
      this.#finishResolve = resolve;
      this.#finishReject = reject;
    });
    socket.send(JSON.stringify({
      version: protocolVersion,
      type: "finish",
      lastSequenceNo: this.#sequenceNo,
    }));
    this.#clearHeartbeat();
    this.#finishTimer = window.setTimeout(() => {
      const error = new Error("等待实时转写结束确认超时");
      this.#finishReject?.(error);
      this.#finishReject = undefined;
      this.#finishResolve = undefined;
      this.#intentionalClose = true;
      socket.close(4000, "finish timeout");
    }, finishTimeoutMs);
    return this.#finishPromise;
  }

  close(): void {
    this.#intentionalClose = true;
    this.#clearTimers();
    const error = new Error("实时转写连接已关闭");
    this.#connectReject?.(error);
    this.#finishReject?.(error);
    this.#clearPendingPromises();
    const socket = this.#socket;
    if (socket && socket.readyState < WebSocket.CLOSING) {
      socket.close(1000, "client cleanup");
    }
    this.#socket = undefined;
    this.#ready = false;
    this.#unacknowledged.clear();
    this.#setState("closed");
  }

  #handleMessage(raw: unknown): void {
    if (typeof raw !== "string") {
      this.#protocolFailure(new Error("实时转写服务返回了非文本控制消息"));
      return;
    }
    try {
      const value: unknown = JSON.parse(raw);
      const message = parseServerMessage(value);
      switch (message.type) {
        case "session_ready":
          this.#handleReady(message);
          return;
        case "ack":
          this.#handleACK(message);
          return;
        case "pong":
          this.#handlePong(message);
          return;
        case "transcript_segment":
          this.options.onSegment(parseSegmentMessage(message));
          return;
        case "error":
          this.#handleServerError(message);
          return;
        case "session_finished":
          this.#handleFinished(message);
          return;
        default:
          throw new Error(`不支持的实时转写消息类型: ${message.type}`);
      }
    } catch (error) {
      this.#protocolFailure(error instanceof Error ? error : new Error("无效的实时转写消息"));
    }
  }

  #handleReady(message: ServerMessage): void {
    if (this.#ready || this.#state !== "connecting" || !isRecord(message.acceptedAudio)) {
      throw new Error("实时转写 ready 消息状态无效");
    }
    if (
      message.acceptedAudio.format !== "pcm" ||
      message.acceptedAudio.sampleRate !== this.options.audio.sampleRate ||
      message.acceptedAudio.channels !== this.options.audio.channels ||
      !isPositiveInteger(message.grantedAudioSeconds)
    ) {
      throw new Error("实时转写服务接受的音频参数不匹配");
    }
    this.#ready = true;
    this.#clearConnectTimer();
    this.#setState("connected");
    this.#connectResolve?.();
    this.#connectResolve = undefined;
    this.#connectReject = undefined;
    this.#startHeartbeat();
  }

  #handleACK(message: ServerMessage): void {
    if (!this.#ready || !isNonNegativeInteger(message.ackSequenceNo) ||
      !isNonNegativeInteger(message.acceptedAudioMs)) {
      throw new Error("实时转写 ACK 字段无效");
    }
    const ackSequenceNo = message.ackSequenceNo;
    if (ackSequenceNo < this.#lastACK || ackSequenceNo > this.#sequenceNo) {
      throw new Error("实时转写 ACK 序号无效");
    }
    this.#lastACK = ackSequenceNo;
    this.#backpressureReported = false;
    for (const sequenceNo of this.#unacknowledged.keys()) {
      if (sequenceNo <= ackSequenceNo) {
        this.#unacknowledged.delete(sequenceNo);
      }
    }
  }

  #handlePong(message: ServerMessage): void {
    if (!isPositiveInteger(message.sentAt) || message.sentAt !== this.#lastPingAt) {
      throw new Error("实时转写 pong 字段无效");
    }
    if (this.#pongTimer !== undefined) {
      window.clearTimeout(this.#pongTimer);
      this.#pongTimer = undefined;
    }
  }

  #handleServerError(message: ServerMessage): void {
    if (
      typeof message.code !== "string" ||
      typeof message.message !== "string" ||
      typeof message.retryable !== "boolean" ||
      !isNonNegativeInteger(message.lastAckSequenceNo)
    ) {
      throw new Error("实时转写错误消息字段无效");
    }
    const error = new RealtimeTranscriptionError(
      message.code,
      message.message,
      message.retryable,
    );
    this.options.onError(error);
    if (!this.#ready) {
      this.#clearConnectTimer();
      this.#connectReject?.(error);
      this.#connectResolve = undefined;
      this.#connectReject = undefined;
    }
  }

  #handleFinished(message: ServerMessage): void {
    if (
      !isNonNegativeInteger(message.lastAckSequenceNo) ||
      !isNonNegativeInteger(message.finalSegmentSequenceNo) ||
      !isNonNegativeInteger(message.acceptedAudioMs) ||
      !isFinishReason(message.finishReason)
    ) {
      throw new Error("实时转写结束消息字段无效");
    }
    if (message.lastAckSequenceNo > this.#sequenceNo) {
      throw new Error("实时转写结束 ACK 序号无效");
    }
    this.#lastACK = message.lastAckSequenceNo;
    this.#unacknowledged.clear();
    if (this.#finishTimer !== undefined) {
      window.clearTimeout(this.#finishTimer);
      this.#finishTimer = undefined;
    }
    this.#finishResolve?.();
    this.#finishResolve = undefined;
    this.#finishReject = undefined;
    this.#intentionalClose = true;
    this.#socket?.close(1000, "transcription finished");
  }

  #handleClose(event: CloseEvent): void {
    this.#clearTimers();
    this.#socket = undefined;
    this.#ready = false;
    const expected = this.#intentionalClose || event.wasClean;
    const error = new Error(expected ? "实时转写连接已关闭" : "实时转写连接意外断开");
    if (!expected) {
      this.options.onError(error);
    }
    this.#connectReject?.(error);
    this.#finishReject?.(expected ? new Error("实时转写连接在结束确认前关闭") : error);
    this.#clearPendingPromises();
    this.#unacknowledged.clear();
    this.#setState(expected ? "closed" : "disconnected");
  }

  #protocolFailure(error: Error): void {
    this.options.onError(error);
    this.#connectReject?.(error);
    this.#finishReject?.(error);
    this.#clearPendingPromises();
    this.#clearTimers();
    this.#setState("disconnected");
    this.#socket?.close(4002, "protocol error");
  }

  #failConnection(error: Error): void {
    if (this.#intentionalClose) {
      return;
    }
    this.options.onError(error);
    this.#connectReject?.(error);
    this.#connectResolve = undefined;
    this.#connectReject = undefined;
    this.#setState("disconnected");
    this.#socket?.close(4000, "connection failure");
  }

  #startHeartbeat(): void {
    this.#heartbeatTimer = window.setInterval(() => {
      const socket = this.#socket;
      if (!socket || socket.readyState !== WebSocket.OPEN || this.#pongTimer !== undefined) {
        return;
      }
      const sentAt = Date.now();
      this.#lastPingAt = sentAt;
      socket.send(JSON.stringify({ version: protocolVersion, type: "ping", sentAt }));
      this.#pongTimer = window.setTimeout(() => {
        this.#pongTimer = undefined;
        this.#protocolFailure(new Error("实时转写心跳超时"));
      }, heartbeatTimeoutMs);
    }, heartbeatIntervalMs);
  }

  #clearConnectTimer(): void {
    if (this.#connectTimer !== undefined) {
      window.clearTimeout(this.#connectTimer);
      this.#connectTimer = undefined;
    }
  }

  #clearHeartbeat(): void {
    if (this.#heartbeatTimer !== undefined) {
      window.clearInterval(this.#heartbeatTimer);
      this.#heartbeatTimer = undefined;
    }
    if (this.#pongTimer !== undefined) {
      window.clearTimeout(this.#pongTimer);
      this.#pongTimer = undefined;
    }
  }

  #clearTimers(): void {
    this.#clearConnectTimer();
    this.#clearHeartbeat();
    if (this.#finishTimer !== undefined) {
      window.clearTimeout(this.#finishTimer);
      this.#finishTimer = undefined;
    }
  }

  #clearPendingPromises(): void {
    this.#connectResolve = undefined;
    this.#connectReject = undefined;
    this.#finishResolve = undefined;
    this.#finishReject = undefined;
  }

  #setState(state: RealtimeConnectionState): void {
    if (this.#state === state) {
      return;
    }
    this.#state = state;
    this.options.onConnectionStateChange(state);
  }
}

function validateAudioConstraints(audio: AudioConstraints): void {
  if (
    audio.mimeType !== pcmMIMEType ||
    audio.sampleRate !== 16_000 ||
    audio.channels !== 1 ||
    !Number.isInteger(audio.chunkDurationMs) ||
    audio.chunkDurationMs < 100 ||
    audio.chunkDurationMs > 200
  ) {
    throw new Error("实时转写音频参数不受支持");
  }
}

function expectedChunkBytes(audio: AudioConstraints): number {
  return ((audio.sampleRate ?? 0) * audio.channels * 2 * audio.chunkDurationMs) / 1_000;
}

function parseServerMessage(value: unknown): ServerMessage {
  if (!isRecord(value) || value.version !== protocolVersion || typeof value.type !== "string") {
    throw new Error("实时转写消息版本或类型无效");
  }
  return value as ServerMessage;
}

function parseSegmentMessage(value: ServerMessage): TranscriptSegment {
  if (
    typeof value.segmentId !== "string" ||
    !isPositiveInteger(value.sequenceNo) ||
    typeof value.content !== "string" ||
    !isNonNegativeInteger(value.startOffsetMs) ||
    !isNonNegativeInteger(value.endOffsetMs) ||
    value.endOffsetMs < value.startOffsetMs ||
    typeof value.isFinal !== "boolean"
  ) {
    throw new Error("实时转写片段字段不完整");
  }
  return {
    id: value.segmentId,
    sequenceNo: value.sequenceNo,
    content: value.content,
    startOffsetMs: value.startOffsetMs,
    endOffsetMs: value.endOffsetMs,
    isFinal: value.isFinal,
    speakerLabel: typeof value.speakerLabel === "string" ? value.speakerLabel : undefined,
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isPositiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function isFinishReason(value: unknown): boolean {
  return value === "client_finished" || value === "quota_exhausted" ||
    value === "cancelled" || value === "expired";
}

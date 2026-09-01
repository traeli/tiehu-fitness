import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TranscriptionClient } from "./transcriptionClient";

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;

  static instances: FakeWebSocket[] = [];

  readonly sent: (string | ArrayBuffer)[] = [];
  readonly listeners = new Map<string, ((event: unknown) => void)[]>();
  binaryType: BinaryType = "blob";
  bufferedAmount = 0;
  readyState = FakeWebSocket.CONNECTING;

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, listener: (event: unknown) => void): void {
    const listeners = this.listeners.get(type) ?? [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  send(data: string | ArrayBuffer): void {
    this.sent.push(data);
  }

  close(): void {
    this.readyState = FakeWebSocket.CLOSED;
  }

  emitOpen(): void {
    this.readyState = FakeWebSocket.OPEN;
    this.emit("open", {});
  }

  emitMessage(value: unknown): void {
    this.emit("message", { data: JSON.stringify(value) });
  }

  private emit(type: string, event: unknown): void {
    this.listeners.get(type)?.forEach((listener) => listener(event));
  }
}

describe("TranscriptionClient", () => {
  beforeEach(() => {
    FakeWebSocket.instances = [];
    vi.stubGlobal("window", globalThis);
    vi.stubGlobal("WebSocket", FakeWebSocket);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("waits for session_ready, sends PCM framing, and completes the finish handshake", async () => {
    const segments: string[] = [];
    const errors: Error[] = [];
    const states: string[] = [];
    const client = new TranscriptionClient({
      url: "ws://127.0.0.1:8100/v1/realtime/transcriptions",
      sessionTicket: "one-time-ticket",
      audio: {
        mimeType: "audio/pcm;rate=16000",
        sampleRate: 16_000,
        channels: 1,
        chunkDurationMs: 200,
      },
      onSegment: (segment) => segments.push(segment.content),
      onError: (error) => errors.push(error),
      onConnectionStateChange: (state) => states.push(state),
    });

    let connected = false;
    const connecting = client.connect().then(() => {
      connected = true;
    });
    const socket = requiredSocket();
    socket.emitOpen();
    await Promise.resolve();
    expect(connected).toBe(false);
    expect(JSON.parse(requiredString(socket.sent[0]))).toMatchObject({
      version: 1,
      type: "start",
      sessionTicket: "one-time-ticket",
    });

    socket.emitMessage({
      version: 1,
      type: "session_ready",
      sessionId: "session-id",
      acceptedAudio: { format: "pcm", sampleRate: 16_000, channels: 1 },
      grantedAudioSeconds: 60,
    });
    await connecting;
    expect(states).toEqual(["connecting", "connected"]);

    const pcm = new ArrayBuffer(6_400);
    expect(client.sendAudioChunk(pcm, 1_787_800_000_000)).toBe(true);
    const frame = new Uint8Array(requiredArrayBuffer(socket.sent[1]));
    const newline = frame.indexOf(10);
    expect(newline).toBeGreaterThan(0);
    const header = JSON.parse(new TextDecoder().decode(frame.slice(0, newline)));
    expect(header).toMatchObject({
      version: 1,
      type: "audio_chunk",
      sequenceNo: 1,
      mimeType: "audio/pcm;rate=16000",
    });
    expect(frame.byteLength - newline - 1).toBe(6_400);

    socket.emitMessage({
      version: 1,
      type: "ack",
      ackSequenceNo: 1,
      acceptedAudioMs: 200,
    });
    socket.emitMessage({
      version: 1,
      type: "transcript_segment",
      segmentId: "segment-id",
      sequenceNo: 1,
      revision: 1,
      startOffsetMs: 0,
      endOffsetMs: 200,
      speakerLabel: null,
      language: "zh",
      content: "本地模拟文本",
      isFinal: true,
    });
    expect(segments).toEqual(["本地模拟文本"]);

    const finishing = client.finish();
    expect(JSON.parse(requiredString(socket.sent[2]))).toEqual({
      version: 1,
      type: "finish",
      lastSequenceNo: 1,
    });
    socket.emitMessage({
      version: 1,
      type: "session_finished",
      lastAckSequenceNo: 1,
      finalSegmentSequenceNo: 1,
      acceptedAudioMs: 200,
      finishReason: "client_finished",
    });
    await finishing;
    expect(errors).toEqual([]);
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);
  });

  it("rejects connect when the server rejects the one-time ticket", async () => {
    const errors: Error[] = [];
    const client = new TranscriptionClient({
      url: "ws://127.0.0.1:8100/v1/realtime/transcriptions",
      sessionTicket: "replayed-ticket",
      audio: {
        mimeType: "audio/pcm;rate=16000",
        sampleRate: 16_000,
        channels: 1,
        chunkDurationMs: 200,
      },
      onSegment: () => undefined,
      onError: (error) => errors.push(error),
      onConnectionStateChange: () => undefined,
    });
    const connecting = client.connect();
    const socket = requiredSocket();
    socket.emitOpen();
    socket.emitMessage({
      version: 1,
      type: "error",
      code: "TRANSCRIPTION_TICKET_INVALID",
      message: "凭证无效",
      retryable: false,
      lastAckSequenceNo: 0,
    });
    await expect(connecting).rejects.toMatchObject({ code: "TRANSCRIPTION_TICKET_INVALID" });
    expect(errors).toHaveLength(1);
    client.close();
  });

  it("returns a stable error when production session preparation exceeds its bound", async () => {
    vi.useFakeTimers();
    try {
      const errors: Error[] = [];
      const client = new TranscriptionClient({
        url: "wss://vision.example.com/v1/realtime/transcriptions",
        sessionTicket: "slow-session-ticket",
        audio: {
          mimeType: "audio/pcm;rate=16000",
          sampleRate: 16_000,
          channels: 1,
          chunkDurationMs: 200,
        },
        onSegment: () => undefined,
        onError: (error) => errors.push(error),
        onConnectionStateChange: () => undefined,
      });

      const connecting = client.connect();
      requiredSocket().emitOpen();
      const rejection = expect(connecting).rejects.toMatchObject({
        code: "TRANSCRIPTION_SESSION_READY_TIMEOUT",
        retryable: true,
      });
      await vi.advanceTimersByTimeAsync(45_000);
      await rejection;
      expect(errors).toHaveLength(1);
      expect(requiredSocket().readyState).toBe(FakeWebSocket.CLOSED);
    } finally {
      vi.useRealTimers();
    }
  });
});

function requiredSocket(): FakeWebSocket {
  const socket = FakeWebSocket.instances[0];
  if (!socket) {
    throw new Error("expected a WebSocket instance");
  }
  return socket;
}

function requiredString(value: string | ArrayBuffer | undefined): string {
  if (typeof value !== "string") {
    throw new Error("expected a text WebSocket message");
  }
  return value;
}

function requiredArrayBuffer(value: string | ArrayBuffer | undefined): ArrayBuffer {
  if (!(value instanceof ArrayBuffer)) {
    throw new Error("expected a binary WebSocket message");
  }
  return value;
}

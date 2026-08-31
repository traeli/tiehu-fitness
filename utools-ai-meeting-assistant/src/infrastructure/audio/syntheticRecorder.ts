import type { AudioConstraints } from "@/domain/meeting";
import type { AudioChunkHandler, AudioRecorder } from "./microphoneRecorder";

const pcmBytesPerSample = 2;

// SyntheticPCMRecorder is an explicit local-browser test adapter. It emits
// bounded silent PCM frames so the real WebSocket/ACK/finish path can be
// exercised without requesting microphone permission or retaining audio.
export class SyntheticPCMRecorder implements AudioRecorder {
  #timer?: number;

  constructor(private readonly audio: AudioConstraints) {
    syntheticChunkBytes(audio);
  }

  async start(onChunk: AudioChunkHandler): Promise<void> {
    if (this.#timer !== undefined) {
      throw new Error("Synthetic audio recorder is already running");
    }
    const chunkBytes = syntheticChunkBytes(this.audio);
    this.#timer = window.setInterval(() => {
      onChunk(new ArrayBuffer(chunkBytes), Date.now());
    }, this.audio.chunkDurationMs);
  }

  async stop(): Promise<undefined> {
    if (this.#timer !== undefined) {
      window.clearInterval(this.#timer);
      this.#timer = undefined;
    }
    return undefined;
  }
}

export function syntheticChunkBytes(audio: AudioConstraints): number {
  if (
    audio.mimeType !== "audio/pcm;rate=16000" ||
    audio.sampleRate !== 16_000 ||
    audio.channels !== 1 ||
    !Number.isInteger(audio.chunkDurationMs) ||
    audio.chunkDurationMs < 100 ||
    audio.chunkDurationMs > 200
  ) {
    throw new Error("合成测试音频参数必须为 16kHz 单声道 PCM16LE，分片长度为 100-200ms");
  }
  return (audio.sampleRate * audio.channels * pcmBytesPerSample * audio.chunkDurationMs) / 1_000;
}

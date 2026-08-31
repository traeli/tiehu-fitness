interface PCMProcessorOptions {
  targetSampleRate?: unknown;
  chunkDurationMs?: unknown;
}

interface PCMProcessorConstructorOptions {
  processorOptions?: PCMProcessorOptions;
}

declare const sampleRate: number;
declare function registerProcessor(
  name: string,
  processorConstructor: new (options?: PCMProcessorConstructorOptions) => AudioWorkletProcessor,
): void;

declare abstract class AudioWorkletProcessor {
  readonly port: MessagePort;
  constructor(options?: PCMProcessorConstructorOptions);
  abstract process(inputs: Float32Array[][]): boolean;
}

class PCMWorkletProcessor extends AudioWorkletProcessor {
  readonly #chunkSamples: number;
  readonly #sourcePerTargetSample: number;
  readonly #sourceBuffer: number[] = [];
  readonly #outputSamples: number[] = [];
  #sourcePosition = 0;

  constructor(options?: PCMProcessorConstructorOptions) {
    super(options);
    const targetSampleRate = options?.processorOptions?.targetSampleRate;
    const chunkDurationMs = options?.processorOptions?.chunkDurationMs;
    if (
      typeof targetSampleRate !== "number" ||
      !Number.isInteger(targetSampleRate) ||
      targetSampleRate !== 16_000 ||
      typeof chunkDurationMs !== "number" ||
      !Number.isInteger(chunkDurationMs) ||
      chunkDurationMs < 100 ||
      chunkDurationMs > 200
    ) {
      throw new Error("Invalid PCM worklet configuration");
    }
    this.#chunkSamples = (targetSampleRate * chunkDurationMs) / 1_000;
    this.#sourcePerTargetSample = sampleRate / targetSampleRate;
    this.port.onmessage = (event: MessageEvent<unknown>) => {
      if (isFlushMessage(event.data)) {
        this.#emitChunk(true);
        this.port.postMessage({ type: "flushed" });
      }
    };
  }

  process(inputs: Float32Array[][]): boolean {
    const channel = inputs[0]?.[0];
    if (!channel || channel.length === 0) {
      return true;
    }
    for (const value of channel) {
      this.#sourceBuffer.push(value);
    }
    while (this.#sourcePosition + 1 < this.#sourceBuffer.length) {
      const leftIndex = Math.floor(this.#sourcePosition);
      const fraction = this.#sourcePosition - leftIndex;
      const left = this.#sourceBuffer[leftIndex];
      const right = this.#sourceBuffer[leftIndex + 1];
      if (left === undefined || right === undefined) {
        break;
      }
      this.#outputSamples.push(left + (right - left) * fraction);
      this.#sourcePosition += this.#sourcePerTargetSample;
      this.#emitChunk(false);
    }
    const discard = Math.floor(this.#sourcePosition);
    if (discard > 0) {
      this.#sourceBuffer.splice(0, discard);
      this.#sourcePosition -= discard;
    }
    return true;
  }

  #emitChunk(flush: boolean): void {
    while (this.#outputSamples.length >= this.#chunkSamples || (flush && this.#outputSamples.length > 0)) {
      const sampleCount = flush
        ? Math.min(this.#chunkSamples, this.#outputSamples.length)
        : this.#chunkSamples;
      const samples = this.#outputSamples.splice(0, sampleCount);
      const buffer = new ArrayBuffer(samples.length * 2);
      const view = new DataView(buffer);
      samples.forEach((sample, index) => {
        const normalized = Math.max(-1, Math.min(1, sample));
        const pcm = normalized < 0 ? normalized * 32_768 : normalized * 32_767;
        view.setInt16(index * 2, Math.round(pcm), true);
      });
      this.port.postMessage({ type: "chunk", buffer }, [buffer]);
    }
  }
}

function isFlushMessage(value: unknown): boolean {
  return typeof value === "object" && value !== null && !Array.isArray(value) &&
    (value as Record<string, unknown>).type === "flush";
}

registerProcessor("tiehu-pcm-capture", PCMWorkletProcessor);

export {};

interface NativeAudioSourceOptions {
  sourceSampleRate?: unknown;
}

interface NativeAudioSourceConstructorOptions {
  processorOptions?: NativeAudioSourceOptions;
}

declare const sampleRate: number;
declare function registerProcessor(
  name: string,
  processorConstructor: new (options?: NativeAudioSourceConstructorOptions) => AudioWorkletProcessor,
): void;

declare abstract class AudioWorkletProcessor {
  readonly port: MessagePort;
  constructor(options?: NativeAudioSourceConstructorOptions);
  abstract process(inputs: Float32Array[][], outputs: Float32Array[][]): boolean;
}

const maxBufferedSeconds = 5;

// This worklet converts framed native PCM into a Web Audio source. Keeping the
// final mixer in one graph guarantees local playback and realtime upload use
// the same microphone/system-audio mix.
class NativeSystemAudioSourceProcessor extends AudioWorkletProcessor {
  readonly #sourcePerOutputSample: number;
  readonly #maxBufferedSamples: number;
  readonly #samples: number[] = [];
  #sourcePosition = 0;

  constructor(options?: NativeAudioSourceConstructorOptions) {
    super(options);
    const sourceSampleRate = options?.processorOptions?.sourceSampleRate;
    if (
      typeof sourceSampleRate !== "number" ||
      !Number.isInteger(sourceSampleRate) ||
      sourceSampleRate !== 48_000
    ) {
      throw new Error("Invalid native system audio source sample rate");
    }
    this.#sourcePerOutputSample = sourceSampleRate / sampleRate;
    this.#maxBufferedSamples = sourceSampleRate * maxBufferedSeconds;
    this.port.onmessage = (event: MessageEvent<unknown>) => {
      const buffer = parsePCMBuffer(event.data);
      if (!buffer) {
        return;
      }
      const view = new DataView(buffer);
      for (let offset = 0; offset < view.byteLength; offset += 2) {
        this.#samples.push(view.getInt16(offset, true) / 32_768);
      }
      if (this.#samples.length > this.#maxBufferedSamples) {
        const dropped = this.#samples.length - this.#maxBufferedSamples;
        this.#samples.splice(0, dropped);
        this.#sourcePosition = Math.max(0, this.#sourcePosition - dropped);
        this.port.postMessage({ type: "overrun", droppedSamples: dropped });
      }
    };
  }

  process(_inputs: Float32Array[][], outputs: Float32Array[][]): boolean {
    const output = outputs[0]?.[0];
    if (!output) {
      return true;
    }
    output.fill(0);
    for (let outputIndex = 0; outputIndex < output.length; outputIndex += 1) {
      const leftIndex = Math.floor(this.#sourcePosition);
      const rightIndex = leftIndex + 1;
      const left = this.#samples[leftIndex];
      const right = this.#samples[rightIndex];
      if (left === undefined || right === undefined) {
        break;
      }
      const fraction = this.#sourcePosition - leftIndex;
      output[outputIndex] = left + (right - left) * fraction;
      this.#sourcePosition += this.#sourcePerOutputSample;
    }
    const discard = Math.floor(this.#sourcePosition);
    if (discard > 0) {
      this.#samples.splice(0, discard);
      this.#sourcePosition -= discard;
    }
    return true;
  }
}

function parsePCMBuffer(value: unknown): ArrayBuffer | undefined {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return undefined;
  }
  const message = value as Record<string, unknown>;
  const buffer = message.buffer;
  if (message.type !== "chunk" || !(buffer instanceof ArrayBuffer) || buffer.byteLength % 2 !== 0) {
    return undefined;
  }
  return buffer;
}

registerProcessor("tiehu-native-system-audio-source", NativeSystemAudioSourceProcessor);

export {};

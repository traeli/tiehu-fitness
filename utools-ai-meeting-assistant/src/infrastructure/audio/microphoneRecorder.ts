import nativeSystemAudioWorkletUrl from "./nativeSystemAudioWorklet.ts?worker&url";
import pcmCaptureWorkletUrl from "./pcmCaptureWorklet.ts?worker&url";

import type { AudioConstraints } from "@/domain/meeting";

export type AudioChunkHandler = (chunk: ArrayBuffer, capturedAt: number) => void;
export type AudioFailureHandler = (error: Error) => void;
export type AudioLevelSource = "system" | "mixed";
export type AudioLevelHandler = (
  source: AudioLevelSource,
  level: { peak: number; rms: number },
) => void;

export interface CapturedAudio {
  blob: Blob;
  durationMs: number;
  mimeType: string;
}

export interface AudioRecorder {
  start(
    onChunk: AudioChunkHandler,
    onFailure?: AudioFailureHandler,
    onLevel?: AudioLevelHandler,
  ): Promise<void>;
  stop(): Promise<CapturedAudio | undefined>;
}

export interface MeetingAudioCaptureOptions {
  captureSystemAudio: boolean;
  captureMicrophone: boolean;
}

export interface SystemAudioCaptureGateway {
  startSystemAudioCapture(
    options: { sampleRate: 48000; channels: 1; format: "pcm_s16le" },
    onAudio: (chunk: ArrayBuffer) => void,
    onError: (failure: { code: string; message: string }) => void,
  ): Promise<{ sampleRate: 48000; channels: 1; format: "pcm_s16le" }>;
  stopSystemAudioCapture(): Promise<void>;
}

export type AudioCaptureErrorCode =
  | "SYSTEM_AUDIO_PERMISSION_DENIED"
  | "SYSTEM_AUDIO_UNAVAILABLE";

export class AudioCaptureError extends Error {
  constructor(
    readonly code: AudioCaptureErrorCode,
    message: string,
  ) {
    super(message);
    this.name = "AudioCaptureError";
  }
}

interface PCMWorkletMessage {
  type: "chunk" | "flushed";
  buffer?: ArrayBuffer;
}

const pcmMIMEType = "audio/pcm;rate=16000";
const nativeSystemAudioSampleRate = 48_000 as const;
const flushTimeoutMs = 1_000;
const recordingBitsPerSecond = 64_000;
const maxCapturedRecordingBytes = 256 * 1024 * 1024;
const recordingMIMETypes = ["audio/webm;codecs=opus", "audio/webm", "audio/mp4"] as const;

// BrowserMeetingAudioRecorder owns the microphone stream and native system
// audio source, mixes both in one Web Audio graph, and fans that mono signal to
// realtime PCM and local recording. Native capture remains behind a narrow
// gateway so the renderer never receives arbitrary Node capabilities.
export class BrowserMeetingAudioRecorder implements AudioRecorder {
  #context?: AudioContext;
  #sources: MediaStreamAudioSourceNode[] = [];
  #inputGains: GainNode[] = [];
  #mix?: GainNode;
  #worklet?: AudioWorkletNode;
  #nativeSystemSource?: AudioWorkletNode;
  #nativeSystemAudioRunning = false;
  #mute?: GainNode;
  #recordingDestination?: MediaStreamAudioDestinationNode;
  #streams: MediaStream[] = [];
  #onChunk?: AudioChunkHandler;
  #flushResolve?: () => void;
  #mediaRecorder?: MediaRecorder;
  #recordedChunks: Blob[] = [];
  #recordedBytes = 0;
  #recordingError?: Error;
  #recordingStartedAt?: number;

  constructor(
    private readonly audio: AudioConstraints,
    private readonly capture: MeetingAudioCaptureOptions,
    private readonly systemAudioGateway: SystemAudioCaptureGateway,
  ) {
    validateRealtimeAudioConstraints(audio);
    validateMeetingAudioCaptureOptions(capture);
  }

  async start(
    onChunk: AudioChunkHandler,
    onFailure?: AudioFailureHandler,
    onLevel?: AudioLevelHandler,
  ): Promise<void> {
    if (this.#context || this.#streams.length > 0 || this.#nativeSystemAudioRunning) {
      throw new Error("Meeting audio recorder is already running");
    }
    if (
      (this.capture.captureMicrophone && !navigator.mediaDevices?.getUserMedia) ||
      typeof AudioWorkletNode === "undefined"
    ) {
      throw new Error("当前运行环境不支持 AudioWorklet 音频采集");
    }

    const streams: MediaStream[] = [];
    let context: AudioContext | undefined;
    let nativeSystemAudioRunning = false;
    let nativeStartupFailure: AudioCaptureError | undefined;
    try {
      if (this.capture.captureMicrophone) {
        streams.push(
          await navigator.mediaDevices.getUserMedia({
            audio: {
              channelCount: 1,
              echoCancellation: true,
              noiseSuppression: true,
            },
            video: false,
          }),
        );
      }
      if (
        !this.capture.captureSystemAudio &&
        !streams.some((stream) => stream.getAudioTracks().length > 0)
      ) {
        throw new AudioCaptureError("SYSTEM_AUDIO_UNAVAILABLE", "没有获取到可用的音频输入");
      }

      context = new AudioContext({ sampleRate: nativeSystemAudioSampleRate });
      await Promise.all([
        context.audioWorklet.addModule(pcmCaptureWorkletUrl),
        this.capture.captureSystemAudio
          ? context.audioWorklet.addModule(nativeSystemAudioWorkletUrl)
          : Promise.resolve(),
      ]);
      const mix = context.createGain();
      mix.channelCount = 1;
      mix.channelCountMode = "explicit";
      mix.channelInterpretation = "speakers";

      const bothSourcesEnabled = this.capture.captureSystemAudio && this.capture.captureMicrophone;
      if (this.capture.captureSystemAudio) {
        const nativeSystemSource = new AudioWorkletNode(
          context,
          "tiehu-native-system-audio-source",
          {
            numberOfInputs: 0,
            numberOfOutputs: 1,
            outputChannelCount: [1],
            channelCount: 1,
            channelCountMode: "explicit",
            channelInterpretation: "speakers",
            processorOptions: { sourceSampleRate: nativeSystemAudioSampleRate },
          },
        );
        const gain = context.createGain();
        gain.gain.value = bothSourcesEnabled ? 0.85 : 1;
        nativeSystemSource.connect(gain).connect(mix);
        nativeSystemSource.port.addEventListener("message", (event: MessageEvent<unknown>) => {
          if (isNativeBufferOverrunMessage(event.data)) {
            console.warn("native system audio buffer overrun", event.data.droppedSamples);
          }
        });
        nativeSystemSource.port.start();
        this.#nativeSystemSource = nativeSystemSource;
        this.#inputGains.push(gain);
      }
      streams.forEach((stream) => {
        const audioOnlyStream = new MediaStream(stream.getAudioTracks());
        const source = context!.createMediaStreamSource(audioOnlyStream);
        const gain = context!.createGain();
        gain.gain.value = bothSourcesEnabled ? 0.65 : 1;
        source.connect(gain).connect(mix);
        this.#sources.push(source);
        this.#inputGains.push(gain);
      });

      const worklet = new AudioWorkletNode(context, "tiehu-pcm-capture", {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        outputChannelCount: [1],
        channelCount: 1,
        channelCountMode: "explicit",
        channelInterpretation: "speakers",
        processorOptions: {
          targetSampleRate: this.audio.sampleRate,
          chunkDurationMs: this.audio.chunkDurationMs,
        },
      });
      const mute = context.createGain();
      mute.gain.value = 0;
      const recordingDestination = context.createMediaStreamDestination();
      recordingDestination.channelCount = 1;
      recordingDestination.channelCountMode = "explicit";
      mix.connect(worklet).connect(mute).connect(context.destination);
      mix.connect(recordingDestination);

      worklet.port.addEventListener("message", (event: MessageEvent<unknown>) => {
        const message = parseWorkletMessage(event.data);
        if (!message) {
          return;
        }
        if (message.type === "flushed") {
          this.#flushResolve?.();
          this.#flushResolve = undefined;
          return;
        }
        if (message.buffer && message.buffer.byteLength > 0) {
          onLevel?.("mixed", measurePCM16Level(message.buffer));
          this.#onChunk?.(message.buffer, Date.now());
        }
      });
      worklet.port.start();

      const mediaRecorder = new MediaRecorder(recordingDestination.stream, {
        mimeType: supportedRecordingMIMEType(),
        audioBitsPerSecond: recordingBitsPerSecond,
      });
      mediaRecorder.addEventListener("dataavailable", (event) => {
        if (event.data.size <= 0) {
          return;
        }
        if (this.#recordedBytes + event.data.size > maxCapturedRecordingBytes) {
          this.#recordingError = new Error("本地录音超过 256 MB 限制，已停止保存");
          if (mediaRecorder.state !== "inactive") {
            mediaRecorder.stop();
          }
          return;
        }
        this.#recordedChunks.push(event.data);
        this.#recordedBytes += event.data.size;
      });

      this.#streams = streams;
      this.#context = context;
      this.#mix = mix;
      this.#worklet = worklet;
      this.#mute = mute;
      this.#recordingDestination = recordingDestination;
      this.#onChunk = onChunk;
      await context.resume();
      if (this.capture.captureSystemAudio) {
        const nativeSystemSource = this.#nativeSystemSource;
        if (!nativeSystemSource) {
          throw new AudioCaptureError("SYSTEM_AUDIO_UNAVAILABLE", "系统音频节点没有正确初始化");
        }
        try {
          await this.systemAudioGateway.startSystemAudioCapture(
            { sampleRate: nativeSystemAudioSampleRate, channels: 1, format: "pcm_s16le" },
            (chunk) => {
              if (chunk.byteLength > 0 && chunk.byteLength % 2 === 0) {
                onLevel?.("system", measurePCM16Level(chunk));
                nativeSystemSource.port.postMessage({ type: "chunk", buffer: chunk }, [chunk]);
              }
            },
            (failure) => {
              const error = mapNativeSystemAudioFailure(failure);
              nativeStartupFailure = error;
              this.#recordingError ??= error;
              onFailure?.(error);
            },
          );
        } catch (error) {
          throw mapUnknownNativeSystemAudioFailure(error);
        }
        nativeSystemAudioRunning = true;
        this.#nativeSystemAudioRunning = true;
        if (nativeStartupFailure) {
          throw nativeStartupFailure;
        }
      }
      mediaRecorder.start(1_000);
      this.#mediaRecorder = mediaRecorder;
      this.#recordingStartedAt = Date.now();
    } catch (error) {
      if (nativeSystemAudioRunning) {
        try {
          await this.systemAudioGateway.stopSystemAudioCapture();
        } catch (stopError) {
          console.error("stop native system audio after startup failure", stopError);
        }
      }
      this.#disconnectGraph();
      streams.forEach(stopMediaStream);
      if (context) {
        try {
          await context.close();
        } catch (closeError) {
          console.error("close audio context after startup failure", closeError);
        }
      }
      this.#clearRuntime();
      throw error;
    }
  }

  async stop(): Promise<CapturedAudio | undefined> {
    const context = this.#context;
    const worklet = this.#worklet;
    let captured: CapturedAudio | undefined;
    let stopError: unknown;

    if (this.#nativeSystemAudioRunning) {
      try {
        await this.systemAudioGateway.stopSystemAudioCapture();
      } catch (error) {
        stopError = error;
      }
      this.#nativeSystemAudioRunning = false;
    }
    if (context && worklet) {
      await new Promise<void>((resolve) => {
        let settled = false;
        const finish = () => {
          if (settled) {
            return;
          }
          settled = true;
          window.clearTimeout(timeout);
          resolve();
        };
        const timeout = window.setTimeout(finish, flushTimeoutMs);
        this.#flushResolve = finish;
        worklet.port.postMessage({ type: "flush" });
      });
    }

    try {
      captured = await this.#stopMediaRecorder();
    } catch (error) {
      stopError ??= error;
    }
    this.#disconnectGraph();
    this.#streams.forEach(stopMediaStream);
    if (context) {
      try {
        await context.close();
      } catch (error) {
        stopError ??= error;
      }
    }
    this.#clearRuntime();
    if (stopError !== undefined) {
      throw stopError;
    }
    return captured;
  }

  async #stopMediaRecorder(): Promise<CapturedAudio | undefined> {
    const mediaRecorder = this.#mediaRecorder;
    const startedAt = this.#recordingStartedAt;
    if (!mediaRecorder || startedAt === undefined) {
      return undefined;
    }
    if (mediaRecorder.state !== "inactive") {
      await new Promise<void>((resolve, reject) => {
        mediaRecorder.addEventListener("stop", () => resolve(), { once: true });
        mediaRecorder.addEventListener(
          "error",
          (event) => reject(new Error(`本地录音失败：${event.error.name}`)),
          { once: true },
        );
        mediaRecorder.stop();
      });
    }
    if (this.#recordingError) {
      throw this.#recordingError;
    }
    const blob = new Blob(this.#recordedChunks, { type: mediaRecorder.mimeType });
    if (blob.size === 0) {
      return undefined;
    }
    return {
      blob,
      durationMs: Math.max(1, Date.now() - startedAt),
      mimeType: mediaRecorder.mimeType,
    };
  }

  #disconnectGraph(): void {
    this.#sources.forEach((source) => source.disconnect());
    this.#inputGains.forEach((gain) => gain.disconnect());
    this.#mix?.disconnect();
    this.#worklet?.disconnect();
    this.#nativeSystemSource?.disconnect();
    this.#mute?.disconnect();
    this.#recordingDestination?.disconnect();
    this.#worklet?.port.close();
    this.#nativeSystemSource?.port.close();
  }

  #clearRuntime(): void {
    this.#context = undefined;
    this.#sources = [];
    this.#inputGains = [];
    this.#mix = undefined;
    this.#worklet = undefined;
    this.#nativeSystemSource = undefined;
    this.#nativeSystemAudioRunning = false;
    this.#mute = undefined;
    this.#recordingDestination = undefined;
    this.#streams = [];
    this.#onChunk = undefined;
    this.#flushResolve = undefined;
    this.#mediaRecorder = undefined;
    this.#recordedChunks = [];
    this.#recordedBytes = 0;
    this.#recordingError = undefined;
    this.#recordingStartedAt = undefined;
  }
}

export function validateMeetingAudioCaptureOptions(options: MeetingAudioCaptureOptions): void {
  if (!options.captureSystemAudio && !options.captureMicrophone) {
    throw new Error("系统音频和麦克风至少需要开启一项");
  }
}

export function supportedRecordingMIMEType(): string {
  if (typeof MediaRecorder === "undefined") {
    throw new Error("当前运行环境不支持本地录音回放");
  }
  const supported = recordingMIMETypes.find((mimeType) => MediaRecorder.isTypeSupported(mimeType));
  if (!supported) {
    throw new Error("当前运行环境不支持 WebM 或 MP4 音频录制");
  }
  return supported;
}

function mapNativeSystemAudioFailure(failure: { code: string; message: string }): AudioCaptureError {
  const permissionDenied = failure.code === "SYSTEM_AUDIO_PERMISSION_DENIED";
  return new AudioCaptureError(
    permissionDenied ? "SYSTEM_AUDIO_PERMISSION_DENIED" : "SYSTEM_AUDIO_UNAVAILABLE",
    failure.message || (permissionDenied
      ? "请允许系统音频录制权限后重试"
      : "系统音频采集组件不可用"),
  );
}

function mapUnknownNativeSystemAudioFailure(error: unknown): AudioCaptureError {
  if (error instanceof AudioCaptureError) {
    return error;
  }
  if (typeof error === "object" && error !== null) {
    const code = Reflect.get(error, "code");
    const message = Reflect.get(error, "message");
    return mapNativeSystemAudioFailure({
      code: typeof code === "string" ? code : "SYSTEM_AUDIO_NATIVE_FAILED",
      message: typeof message === "string" ? message : "系统音频采集组件不可用",
    });
  }
  return new AudioCaptureError("SYSTEM_AUDIO_UNAVAILABLE", "系统音频采集组件不可用");
}

export function measurePCM16Level(buffer: ArrayBuffer): { peak: number; rms: number } {
  if (buffer.byteLength === 0 || buffer.byteLength % 2 !== 0) {
    return { peak: 0, rms: 0 };
  }
  const view = new DataView(buffer);
  let peak = 0;
  let sumSquares = 0;
  const sampleCount = view.byteLength / 2;
  for (let offset = 0; offset < view.byteLength; offset += 2) {
    const normalized = view.getInt16(offset, true) / 32_768;
    const absolute = Math.abs(normalized);
    peak = Math.max(peak, absolute);
    sumSquares += normalized * normalized;
  }
  return { peak, rms: Math.sqrt(sumSquares / sampleCount) };
}

function isNativeBufferOverrunMessage(
  value: unknown,
): value is { type: "overrun"; droppedSamples: number } {
  return typeof value === "object" && value !== null && !Array.isArray(value) &&
    Reflect.get(value, "type") === "overrun" &&
    Number.isSafeInteger(Reflect.get(value, "droppedSamples"));
}

function validateRealtimeAudioConstraints(audio: AudioConstraints): void {
  if (
    audio.mimeType !== pcmMIMEType ||
    audio.sampleRate !== 16_000 ||
    audio.channels !== 1 ||
    audio.chunkDurationMs < 100 ||
    audio.chunkDurationMs > 200
  ) {
    throw new Error("实时音频参数必须为 16kHz 单声道 PCM16LE，分片长度为 100-200ms");
  }
}

function stopMediaStream(stream: MediaStream): void {
  stream.getTracks().forEach((track) => track.stop());
}

function parseWorkletMessage(value: unknown): PCMWorkletMessage | undefined {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return undefined;
  }
  const messageType = Reflect.get(value, "type");
  if (messageType === "flushed") {
    return { type: "flushed" };
  }
  const buffer = Reflect.get(value, "buffer");
  if (messageType === "chunk" && buffer instanceof ArrayBuffer) {
    return { type: "chunk", buffer };
  }
  return undefined;
}

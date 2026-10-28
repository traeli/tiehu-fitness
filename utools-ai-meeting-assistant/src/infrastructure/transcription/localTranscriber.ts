import type { TranscriptSegment } from "@/domain/meeting";

const sampleRate = 16_000;
const bytesPerSample = 2;
const chunkDurationSeconds = 10;
const targetChunkBytes = sampleRate * bytesPerSample * chunkDurationSeconds;
const minimumFinalChunkBytes = sampleRate * bytesPerSample / 2;
const analysisFrameSamples = sampleRate / 50;
const minimumPeakAmplitude = 0.015;
const activeFrameRMS = 0.006;
const minimumActiveFrames = 6;

export interface LocalTranscriptionGateway {
  startLocalTranscription(input: { modelId: LocalTranscriptionModelID }): Promise<void>;
  transcribeLocalAudio(input: { pcmData: ArrayBuffer }): Promise<Array<{
    startMs: number;
    endMs: number;
    text: string;
  }>>;
  stopLocalTranscription(): Promise<void>;
}

export class LocalTranscriber {
  private buffered: Uint8Array[] = [];
  private bufferedBytes = 0;
  private processedBytes = 0;
  private queue = Promise.resolve();
  private sequenceNo = 0;
  private started = false;

  constructor(
    private readonly gateway: LocalTranscriptionGateway,
    private readonly modelId: LocalTranscriptionModelID,
    private readonly onSegment: (segment: TranscriptSegment) => void,
  ) {}

  async start(): Promise<void> {
    await this.gateway.startLocalTranscription({ modelId: this.modelId });
    this.started = true;
  }

  push(pcmData: ArrayBuffer): void {
    if (!this.started || pcmData.byteLength === 0) return;
    const copy = new Uint8Array(pcmData.slice(0));
    this.buffered.push(copy);
    this.bufferedBytes += copy.byteLength;
    while (this.bufferedBytes >= targetChunkBytes) {
      this.enqueue(this.take(targetChunkBytes));
    }
  }

  async finish(): Promise<void> {
    if (!this.started) return;
    if (this.bufferedBytes >= minimumFinalChunkBytes) {
      this.enqueue(this.take(this.bufferedBytes));
    }
    await this.queue;
    await this.gateway.stopLocalTranscription();
    this.started = false;
  }

  close(): void {
    this.started = false;
    this.buffered = [];
    this.bufferedBytes = 0;
    void this.gateway.stopLocalTranscription();
  }

  private enqueue(pcmData: ArrayBuffer): void {
    const baseOffsetMs = Math.round(this.processedBytes / bytesPerSample / sampleRate * 1_000);
    this.processedBytes += pcmData.byteLength;
    if (!containsLikelySpeech(pcmData)) return;
    this.queue = this.queue.then(async () => {
      const segments = await this.gateway.transcribeLocalAudio({ pcmData });
      for (const segment of segments) {
        const content = segment.text.trim();
        if (!content || isLikelyWhisperHallucination(content)) continue;
        const sequenceNo = this.sequenceNo++;
        this.onSegment({
          id: crypto.randomUUID(),
          sequenceNo,
          startOffsetMs: baseOffsetMs + Math.max(0, Math.round(segment.startMs)),
          endOffsetMs: baseOffsetMs + Math.max(0, Math.round(segment.endMs)),
          content,
          isFinal: true,
        });
      }
    });
  }

  private take(byteCount: number): ArrayBuffer {
    const output = new Uint8Array(byteCount);
    let outputOffset = 0;
    while (outputOffset < byteCount) {
      const current = this.buffered[0];
      if (!current) throw new Error("本地转写音频缓冲区异常");
      const copyBytes = Math.min(current.byteLength, byteCount - outputOffset);
      output.set(current.subarray(0, copyBytes), outputOffset);
      outputOffset += copyBytes;
      if (copyBytes === current.byteLength) {
        this.buffered.shift();
      } else {
        this.buffered[0] = current.subarray(copyBytes);
      }
      this.bufferedBytes -= copyBytes;
    }
    return output.buffer;
  }
}

export function containsLikelySpeech(pcmData: ArrayBuffer): boolean {
  if (pcmData.byteLength < bytesPerSample || pcmData.byteLength % bytesPerSample !== 0) return false;
  const samples = new Int16Array(pcmData);
  let activeFrames = 0;
  let peak = 0;
  for (let frameStart = 0; frameStart < samples.length; frameStart += analysisFrameSamples) {
    const frameEnd = Math.min(samples.length, frameStart + analysisFrameSamples);
    let squareSum = 0;
    for (let index = frameStart; index < frameEnd; index += 1) {
      const sample = samples[index];
      if (sample === undefined) continue;
      const normalized = sample / 32768;
      const absolute = Math.abs(normalized);
      peak = Math.max(peak, absolute);
      squareSum += normalized * normalized;
    }
    const frameSamples = frameEnd - frameStart;
    if (frameSamples > 0 && Math.sqrt(squareSum / frameSamples) >= activeFrameRMS) activeFrames += 1;
  }
  return peak >= minimumPeakAmplitude && activeFrames >= minimumActiveFrames;
}

export function isLikelyWhisperHallucination(content: string): boolean {
  const normalized = content.toLowerCase().replace(/[\s\p{P}\p{S}]+/gu, "");
  const engagementTerms = ["点赞", "订阅", "转发", "打赏"];
  const matchedTerms = engagementTerms.filter((term) => normalized.includes(term));
  return normalized.includes("请不吝点赞") && matchedTerms.length >= 3;
}

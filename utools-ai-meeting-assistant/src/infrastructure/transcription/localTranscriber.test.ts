import { describe, expect, it, vi } from "vitest";

import {
  containsLikelySpeech,
  isLikelyWhisperHallucination,
  LocalTranscriber,
  type LocalTranscriptionGateway,
} from "./localTranscriber";

function audiblePCM(durationSeconds: number): ArrayBuffer {
  const samples = new Int16Array(16_000 * durationSeconds);
  for (let index = 0; index < samples.length; index += 1) samples[index] = index % 2 === 0 ? 1_200 : -1_200;
  return samples.buffer;
}

describe("LocalTranscriber", () => {
  it("buffers ten seconds of PCM and maps local offsets to meeting offsets", async () => {
    const gateway: LocalTranscriptionGateway = {
      startLocalTranscription: vi.fn(async () => undefined),
      transcribeLocalAudio: vi.fn(async () => [{ startMs: 100, endMs: 900, text: " 本地转写 " }]),
      stopLocalTranscription: vi.fn(async () => undefined),
    };
    const onSegment = vi.fn();
    const transcriber = new LocalTranscriber(gateway, "small-q5_1", onSegment);

    await transcriber.start();
    transcriber.push(audiblePCM(10));
    await transcriber.finish();

    expect(gateway.transcribeLocalAudio).toHaveBeenCalledTimes(1);
    expect(gateway.startLocalTranscription).toHaveBeenCalledWith({ modelId: "small-q5_1" });
    expect(onSegment).toHaveBeenCalledWith(expect.objectContaining({
      sequenceNo: 0,
      startOffsetMs: 100,
      endOffsetMs: 900,
      content: "本地转写",
      isFinal: true,
    }));
    expect(gateway.stopLocalTranscription).toHaveBeenCalledOnce();
  });

  it("flushes a final half-second chunk but ignores shorter trailing noise", async () => {
    const transcribeLocalAudio = vi.fn(async () => []);
    const gateway: LocalTranscriptionGateway = {
      startLocalTranscription: async () => undefined,
      transcribeLocalAudio,
      stopLocalTranscription: async () => undefined,
    };

    const enough = new LocalTranscriber(gateway, "small-q5_1", () => undefined);
    await enough.start();
    enough.push(audiblePCM(0.5));
    await enough.finish();
    expect(transcribeLocalAudio).toHaveBeenCalledTimes(1);

    const tooShort = new LocalTranscriber(gateway, "small-q5_1", () => undefined);
    await tooShort.start();
    tooShort.push(new ArrayBuffer(15_998));
    await tooShort.finish();
    expect(transcribeLocalAudio).toHaveBeenCalledTimes(1);
  });

  it("does not send silent audio to Whisper", async () => {
    const transcribeLocalAudio = vi.fn(async () => [{ startMs: 0, endMs: 1_000, text: "虚假文字" }]);
    const onSegment = vi.fn();
    const transcriber = new LocalTranscriber({
      startLocalTranscription: async () => undefined,
      transcribeLocalAudio,
      stopLocalTranscription: async () => undefined,
    }, "small-q5_1", onSegment);

    await transcriber.start();
    transcriber.push(new ArrayBuffer(16_000 * 2 * 10));
    await transcriber.finish();

    expect(transcribeLocalAudio).not.toHaveBeenCalled();
    expect(onSegment).not.toHaveBeenCalled();
  });

  it("drops the known engagement-call hallucination", async () => {
    const onSegment = vi.fn();
    const transcriber = new LocalTranscriber({
      startLocalTranscription: async () => undefined,
      transcribeLocalAudio: async () => [{
        startMs: 0,
        endMs: 1_000,
        text: "请不吝点赞、订阅、转发、打赏，打开打开打开的小伙伴们。",
      }],
      stopLocalTranscription: async () => undefined,
    }, "small-q5_1", onSegment);

    await transcriber.start();
    transcriber.push(audiblePCM(10));
    await transcriber.finish();

    expect(onSegment).not.toHaveBeenCalled();
  });
});

describe("local speech filtering", () => {
  it("distinguishes silence from an audible signal", () => {
    expect(containsLikelySpeech(new ArrayBuffer(16_000 * 2))).toBe(false);
    expect(containsLikelySpeech(audiblePCM(1))).toBe(true);
  });

  it("only filters the specific multi-action hallucination pattern", () => {
    expect(isLikelyWhisperHallucination("请不吝点赞、订阅、转发、打赏")).toBe(true);
    expect(isLikelyWhisperHallucination("请大家点赞支持这个项目")).toBe(false);
  });
});

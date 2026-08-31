import { describe, expect, it } from "vitest";

import { syntheticChunkBytes } from "./syntheticRecorder";

describe("syntheticChunkBytes", () => {
  it("matches the backend 200ms PCM frame limit", () => {
    expect(syntheticChunkBytes({
      mimeType: "audio/pcm;rate=16000",
      sampleRate: 16_000,
      channels: 1,
      chunkDurationMs: 200,
    })).toBe(6_400);
  });

  it("rejects unsupported audio constraints", () => {
    expect(() => syntheticChunkBytes({
      mimeType: "audio/webm",
      sampleRate: 48_000,
      channels: 2,
      chunkDurationMs: 200,
    })).toThrow("合成测试音频参数");
  });
});

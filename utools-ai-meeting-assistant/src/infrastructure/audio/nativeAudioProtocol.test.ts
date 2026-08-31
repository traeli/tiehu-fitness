import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";

interface NativeAudioFrame {
  type: number;
  payload: Buffer;
}

interface NativeAudioDecoder {
  push(chunk: Buffer): NativeAudioFrame[];
}

const require = createRequire(import.meta.url);
const protocol = require("../../../plugin/native-audio-protocol.cjs") as {
  NativeAudioFrameDecoder: new () => NativeAudioDecoder;
  nativeAudioFrameTypes: { READY: number; AUDIO: number; ERROR: number };
};
const processModule = require("../../../plugin/native-audio-process.cjs") as {
  resolveNativeAudioExecutable(base: string, platform: string, arch: string): string;
};

describe("native audio frame decoder", () => {
  it("decodes split and coalesced frames without losing PCM bytes", () => {
    const ready = encodeFrame(
      protocol.nativeAudioFrameTypes.READY,
      Buffer.from('{"sampleRate":48000,"channels":1,"format":"pcm_s16le"}'),
    );
    const pcm = Buffer.from([0x00, 0x00, 0xff, 0x7f]);
    const audio = encodeFrame(protocol.nativeAudioFrameTypes.AUDIO, pcm);
    const decoder = new protocol.NativeAudioFrameDecoder();

    expect(decoder.push(ready.subarray(0, 7))).toEqual([]);
    const frames = decoder.push(Buffer.concat([ready.subarray(7), audio]));

    expect(frames).toHaveLength(2);
    expect(frames[0]?.type).toBe(protocol.nativeAudioFrameTypes.READY);
    expect(frames[1]?.payload).toEqual(pcm);
  });

  it("rejects an invalid magic before data reaches the renderer", () => {
    const decoder = new protocol.NativeAudioFrameDecoder();
    const invalid = encodeFrame(protocol.nativeAudioFrameTypes.AUDIO, Buffer.from([0, 0]));
    invalid[0] = 0;

    expect(() => decoder.push(invalid)).toThrow("magic");
  });
});

describe("native audio executable resolution", () => {
  it("maps supported macOS and Windows architectures to fixed paths", () => {
    expect(processModule.resolveNativeAudioExecutable("/plugin", "darwin", "arm64"))
      .toBe("/plugin/native/darwin-arm64/tiehu-system-audio");
    expect(processModule.resolveNativeAudioExecutable("C:\\plugin", "win32", "x64"))
      .toContain("win32-x64");
  });

  it("rejects unsupported platforms", () => {
    expect(() => processModule.resolveNativeAudioExecutable("/plugin", "linux", "x64"))
      .toThrow("暂不支持");
  });
});

function encodeFrame(type: number, payload: Buffer): Buffer {
  const header = Buffer.alloc(12);
  header.write("THAU", 0, "ascii");
  header.writeUInt8(1, 4);
  header.writeUInt8(type, 5);
  header.writeUInt32LE(payload.length, 8);
  return Buffer.concat([header, payload]);
}

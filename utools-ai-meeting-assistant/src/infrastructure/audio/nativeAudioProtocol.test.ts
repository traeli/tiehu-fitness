import { createRequire } from "node:module";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";

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
  prepareNativeAudioExecutable(
    base: string,
    runtimeDirectory: string | undefined,
    platform: string,
    arch: string,
  ): Promise<string>;
  resolveNativeAudioExecutable(base: string, platform: string, arch: string): string;
};
const temporaryDirectories: string[] = [];

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map((directory) => rm(directory, {
    recursive: true,
    force: true,
  })));
});

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

  it("materializes a packaged Windows helper whenever a writable runtime directory is provided", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "tiehu-native-audio-"));
    temporaryDirectories.push(root);
    const bundle = path.join(root, "plugin.asar");
    const source = processModule.resolveNativeAudioExecutable(bundle, "win32", "x64");
    const runtime = path.join(root, "runtime");
    const content = Buffer.from("MZ-native-audio-test", "ascii");
    await mkdir(path.dirname(source), { recursive: true });
    await writeFile(source, content);

    const executable = await processModule.prepareNativeAudioExecutable(
      bundle,
      runtime,
      "win32",
      "x64",
    );

    expect(executable).not.toBe(source);
    expect(executable.startsWith(runtime)).toBe(true);
    expect(await readFile(executable)).toEqual(content);
    expect(await processModule.prepareNativeAudioExecutable(bundle, runtime, "win32", "x64"))
      .toBe(executable);
  });

  it("uses a source-tree helper directly when no runtime directory is provided", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "tiehu-native-audio-source-"));
    temporaryDirectories.push(root);
    const source = processModule.resolveNativeAudioExecutable(root, "win32", "x64");
    await mkdir(path.dirname(source), { recursive: true });
    await writeFile(source, Buffer.from("MZ-native-audio-source", "ascii"));

    expect(await processModule.prepareNativeAudioExecutable(root, undefined, "win32", "x64"))
      .toBe(source);
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

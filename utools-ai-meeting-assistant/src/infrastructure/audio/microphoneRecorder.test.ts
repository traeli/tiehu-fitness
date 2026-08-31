import { afterEach, describe, expect, it, vi } from "vitest";

import {
  measurePCM16Level,
  supportedRecordingMIMEType,
  validateMeetingAudioCaptureOptions,
} from "./microphoneRecorder";

const originalMediaRecorder = globalThis.MediaRecorder;

afterEach(() => {
  Object.defineProperty(globalThis, "MediaRecorder", {
    configurable: true,
    value: originalMediaRecorder,
  });
});

describe("supportedRecordingMIMEType", () => {
  it("prefers Opus WebM when the browser supports it", () => {
    const isTypeSupported = vi.fn((mimeType: string) => mimeType === "audio/webm;codecs=opus");
    Object.defineProperty(globalThis, "MediaRecorder", {
      configurable: true,
      value: { isTypeSupported },
    });

    expect(supportedRecordingMIMEType()).toBe("audio/webm;codecs=opus");
  });

  it("rejects browsers without a playable recording format", () => {
    Object.defineProperty(globalThis, "MediaRecorder", {
      configurable: true,
      value: { isTypeSupported: () => false },
    });

    expect(() => supportedRecordingMIMEType()).toThrow("不支持 WebM 或 MP4");
  });
});

describe("validateMeetingAudioCaptureOptions", () => {
  it("requires at least one real audio source", () => {
    expect(() =>
      validateMeetingAudioCaptureOptions({
        captureSystemAudio: false,
        captureMicrophone: false,
      }),
    ).toThrow("至少需要开启一项");
  });

  it("accepts system audio with optional microphone mixing", () => {
    expect(() =>
      validateMeetingAudioCaptureOptions({
        captureSystemAudio: true,
        captureMicrophone: true,
      }),
    ).not.toThrow();
  });
});

describe("measurePCM16Level", () => {
  it("reports silence without manufacturing a signal", () => {
    expect(measurePCM16Level(new ArrayBuffer(8))).toEqual({ peak: 0, rms: 0 });
  });

  it("reports peak and RMS for native little-endian PCM", () => {
    const buffer = new ArrayBuffer(8);
    const view = new DataView(buffer);
    view.setInt16(0, 16_384, true);
    view.setInt16(2, -16_384, true);
    view.setInt16(4, 0, true);
    view.setInt16(6, 0, true);

    const level = measurePCM16Level(buffer);
    expect(level.peak).toBe(0.5);
    expect(level.rms).toBeCloseTo(Math.sqrt(0.125));
  });
});

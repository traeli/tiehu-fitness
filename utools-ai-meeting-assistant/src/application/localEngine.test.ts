import { describe, expect, it } from "vitest";

import { modelDownloadPercent, resolveLocalEngineSetupState } from "./localEngine";

const readyStatus: LocalTranscriptionStatus = {
  supported: true,
  binaryAvailable: true,
  modelDownloaded: true,
  running: false,
  selectedModelId: "small-q5_1",
  modelName: "Whisper Small Q5",
  modelSizeBytes: 190_085_487,
  models: [{
    id: "small-q5_1",
    name: "Whisper Small Q5",
    description: "中文会议效果更均衡，推荐使用",
    sizeBytes: 190_085_487,
    downloaded: true,
    recommended: true,
  }],
};

describe("local engine setup state", () => {
  it.each([
    [undefined, "idle", false, "checking"],
    [{ ...readyStatus, supported: false }, "idle", false, "unsupported"],
    [{ ...readyStatus, binaryAvailable: false }, "idle", false, "runtime_missing"],
    [{ ...readyStatus, modelDownloaded: false }, "idle", false, "model_required"],
    [readyStatus, "downloading", false, "downloading"],
    [readyStatus, "verifying", false, "verifying"],
    [readyStatus, "idle", true, "failed"],
    [readyStatus, "idle", false, "ready"],
  ] as const)("maps engine inputs to %s", (status, operationPhase, failed, expected) => {
    expect(resolveLocalEngineSetupState({ status, operationPhase, failed })).toBe(expected);
  });

  it("bounds model download progress", () => {
    expect(modelDownloadPercent(50, 100)).toBe(50);
    expect(modelDownloadPercent(120, 100)).toBe(100);
    expect(modelDownloadPercent(1, 0)).toBe(0);
  });
});

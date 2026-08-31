import { describe, expect, it, vi } from "vitest";

import {
  DesktopFileRecordingRepository,
  type DesktopRecordingBridge,
} from "./desktopFileRecordingRepository";

describe("DesktopFileRecordingRepository", () => {
  it("passes recording bytes to preload and rebuilds a playable Blob on demand", async () => {
    const audioData = new Uint8Array([1, 2, 3, 4]).buffer;
    const bridge: DesktopRecordingBridge = {
      saveRecording: vi.fn(async () => undefined),
      listRecordings: vi.fn(async () => [
        {
          id: "recording-id",
          meetingId: "meeting-id",
          createdAt: "2026-08-29T08:00:00.000Z",
          durationMs: 1_200,
          mimeType: "audio/webm",
          sizeBytes: 4,
        },
      ]),
      readRecording: vi.fn(async () => ({ mimeType: "audio/webm", audioData })),
      deleteRecording: vi.fn(async () => undefined),
    };
    const repository = new DesktopFileRecordingRepository(bridge);

    await repository.save({
      id: "recording-id",
      meetingId: "meeting-id",
      createdAt: "2026-08-29T08:00:00.000Z",
      durationMs: 1_200,
      mimeType: "audio/webm",
      audio: new Blob([audioData], { type: "audio/webm" }),
    });
    const recordings = await repository.list();
    const audio = await repository.loadAudio("recording-id");
    await repository.delete("recording-id");

    expect(recordings).toHaveLength(1);
    expect(new Uint8Array(await audio.arrayBuffer())).toEqual(new Uint8Array([1, 2, 3, 4]));
    expect(bridge.saveRecording).toHaveBeenCalledWith(
      expect.objectContaining({ id: "recording-id", audioData: expect.any(ArrayBuffer) }),
    );
    expect(bridge.deleteRecording).toHaveBeenCalledWith("recording-id");
  });
});

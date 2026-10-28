import type {
  LocalMeetingRecording,
  RecordingRepository,
  SaveLocalMeetingRecording,
} from "./recordingRepository";

export interface DesktopRecordingBridge {
  saveRecording(input: {
    id: string;
    meetingId: string;
    createdAt: string;
    durationMs: number;
    mimeType: string;
    audioData: ArrayBuffer;
    transcript: SaveLocalMeetingRecording["transcript"];
  }): Promise<void>;
  listRecordings(): Promise<LocalMeetingRecording[]>;
  readRecording(id: string): Promise<{ mimeType: string; audioData: ArrayBuffer }>;
  readRecordingTranscript(id: string): Promise<SaveLocalMeetingRecording["transcript"]>;
  readRecordingSummary(id: string): Promise<import("@/domain/meeting").LocalMeetingSummary | null>;
  saveRecordingSummary(id: string, summary: import("@/domain/meeting").LocalMeetingSummary): Promise<void>;
  deleteRecording(id: string): Promise<void>;
}

export class DesktopFileRecordingRepository implements RecordingRepository {
  constructor(private readonly bridge: DesktopRecordingBridge) {}

  async save(recording: SaveLocalMeetingRecording): Promise<void> {
    const audioData = await recording.audio.arrayBuffer();
    await this.bridge.saveRecording({
      id: recording.id,
      meetingId: recording.meetingId,
      createdAt: recording.createdAt,
      durationMs: recording.durationMs,
      mimeType: recording.mimeType,
      audioData,
      transcript: recording.transcript,
    });
  }

  async list(): Promise<LocalMeetingRecording[]> {
    return this.bridge.listRecordings();
  }

  async loadAudio(id: string): Promise<Blob> {
    const recording = await this.bridge.readRecording(id);
    return new Blob([recording.audioData], { type: recording.mimeType });
  }

  async loadTranscript(id: string): Promise<SaveLocalMeetingRecording["transcript"]> {
    return this.bridge.readRecordingTranscript(id);
  }

  async loadSummary(id: string): Promise<import("@/domain/meeting").LocalMeetingSummary | undefined> {
    return (await this.bridge.readRecordingSummary(id)) ?? undefined;
  }

  async saveSummary(id: string, summary: import("@/domain/meeting").LocalMeetingSummary): Promise<void> {
    await this.bridge.saveRecordingSummary(id, summary);
  }

  async delete(id: string): Promise<void> {
    await this.bridge.deleteRecording(id);
  }
}

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
  }): Promise<void>;
  listRecordings(): Promise<LocalMeetingRecording[]>;
  readRecording(id: string): Promise<{ mimeType: string; audioData: ArrayBuffer }>;
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
    });
  }

  async list(): Promise<LocalMeetingRecording[]> {
    return this.bridge.listRecordings();
  }

  async loadAudio(id: string): Promise<Blob> {
    const recording = await this.bridge.readRecording(id);
    return new Blob([recording.audioData], { type: recording.mimeType });
  }

  async delete(id: string): Promise<void> {
    await this.bridge.deleteRecording(id);
  }
}

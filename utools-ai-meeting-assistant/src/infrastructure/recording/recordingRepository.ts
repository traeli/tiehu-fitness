export interface LocalMeetingRecording {
  id: string;
  meetingId: string;
  createdAt: string;
  durationMs: number;
  mimeType: string;
  sizeBytes: number;
}

export interface SaveLocalMeetingRecording {
  id: string;
  meetingId: string;
  createdAt: string;
  durationMs: number;
  mimeType: string;
  audio: Blob;
}

export interface RecordingRepository {
  save(recording: SaveLocalMeetingRecording): Promise<void>;
  list(): Promise<LocalMeetingRecording[]>;
  loadAudio(id: string): Promise<Blob>;
  delete(id: string): Promise<void>;
}

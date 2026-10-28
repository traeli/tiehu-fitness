import type { LocalMeetingSummary, TranscriptSegment } from "@/domain/meeting";

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
  transcript: TranscriptSegment[];
}

export interface RecordingRepository {
  save(recording: SaveLocalMeetingRecording): Promise<void>;
  list(): Promise<LocalMeetingRecording[]>;
  loadAudio(id: string): Promise<Blob>;
  loadTranscript(id: string): Promise<TranscriptSegment[]>;
  loadSummary(id: string): Promise<LocalMeetingSummary | undefined>;
  saveSummary(id: string, summary: LocalMeetingSummary): Promise<void>;
  delete(id: string): Promise<void>;
}

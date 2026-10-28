export type MeetingStatus =
  | "recording"
  | "processing"
  | "completed"
  | "partially_completed"
  | "failed"
  | "cancelled";

export type ClientMeetingPhase =
  | "idle"
  | "starting"
  | MeetingStatus
  | "stopping";

export interface TranscriptSegment {
  id: string;
  sequenceNo: number;
  startOffsetMs: number;
  endOffsetMs: number;
  speakerLabel?: string;
  content: string;
  isFinal: boolean;
}

export interface MeetingActionItem {
  assignee: string;
  task: string;
  dueText: string;
  status: "pending";
}

export type MeetingSummaryProvider = "ollama" | "openai" | "deepseek";

export interface LocalMeetingSummary {
  topic: string;
  abstract: string;
  keyDiscussions: string[];
  decisions: string[];
  actionItems: MeetingActionItem[];
  risks: string[];
  provider: MeetingSummaryProvider;
  model: string;
  promptVersion: "meeting-summary-v2-local";
  generatedAt: string;
  inputTokens: number;
  outputTokens: number;
}

export interface AudioConstraints {
  mimeType: string;
  sampleRate?: number;
  channels: number;
  chunkDurationMs: number;
}

export interface MeetingSession {
  meetingId: string;
  status: MeetingStatus;
  startedAt: string;
  audio: AudioConstraints;
}

const terminalStatuses: ReadonlySet<ClientMeetingPhase> = new Set([
  "completed",
  "partially_completed",
  "failed",
  "cancelled",
]);

export function isTerminalPhase(phase: ClientMeetingPhase): boolean {
  return terminalStatuses.has(phase);
}

export function canStartMeeting(phase: ClientMeetingPhase): boolean {
  return phase === "idle" || isTerminalPhase(phase);
}

export function canStopMeeting(phase: ClientMeetingPhase): boolean {
  return phase === "starting" || phase === "recording";
}

export function requiresMeetingCleanup(phase: ClientMeetingPhase): boolean {
  return phase === "starting" || phase === "recording" || phase === "stopping";
}

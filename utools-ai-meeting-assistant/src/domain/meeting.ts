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

export type MeetingSummaryStatus =
  | "not_started"
  | "pending"
  | "processing"
  | "succeeded"
  | "failed";

export interface TranscriptSegment {
  id: string;
  sequenceNo: number;
  startOffsetMs: number;
  endOffsetMs: number;
  speakerLabel?: string;
  content: string;
  isFinal: boolean;
}

export interface ActionItem {
  assignee?: string;
  task: string;
  dueText?: string;
}

export interface MeetingSummary {
  meetingId?: string;
  version?: number;
  sourceTranscriptRevision?: number;
  topic: string;
  abstract: string;
  keyDiscussions: string[];
  decisions: string[];
  actionItems: ActionItem[];
  risks: string[];
  generatedAt?: string;
}

export interface MeetingSummaryResult {
  status: MeetingSummaryStatus;
  summary?: MeetingSummary;
  failureReason?: string;
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
  websocketUrl?: string;
  sessionTicket?: string;
  audio: AudioConstraints;
}

export interface MeetingResult {
  meetingId: string;
  status: MeetingStatus;
  summary?: MeetingSummary;
	summaryStatus?: MeetingSummaryStatus;
}

export interface MeetingQuota {
  periodStart: string;
  periodEnd: string;
  baseLimitSeconds: number;
  purchasedLimitSeconds: number;
  totalLimitSeconds: number;
  consumedSeconds: number;
  reservedSeconds: number;
  remainingSeconds: number;
  maxMeetingSeconds: number;
  maxConcurrentMeetings: number;
  activeMeetings: number;
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
  return phase === "recording";
}

export function requiresMeetingCleanup(phase: ClientMeetingPhase): boolean {
  return phase === "starting" || phase === "recording" || phase === "stopping";
}

export function formatQuotaDuration(seconds: number): string {
  if (!Number.isSafeInteger(seconds) || seconds <= 0) {
    return "0 分钟";
  }
  const hours = Math.floor(seconds / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  const remainingSeconds = seconds % 60;
  const parts: string[] = [];
  if (hours > 0) {
    parts.push(`${hours} 小时`);
  }
  if (minutes > 0) {
    parts.push(`${minutes} 分钟`);
  }
  if (remainingSeconds > 0) {
    parts.push(`${remainingSeconds} 秒`);
  }
  return parts.join(" ");
}

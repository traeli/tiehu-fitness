import type {
  MeetingResult,
  MeetingQuota,
  MeetingSession,
  MeetingStatus,
  MeetingSummary,
  MeetingSummaryResult,
  TranscriptSegment,
} from "@/domain/meeting";

import type { ApiClient } from "./apiClient";

export interface CreateMeetingInput {
  language: "auto" | "zh-CN" | "en-US";
  retainAudio: boolean;
  transcriptionConsent: boolean;
  idempotencyKey: string;
}

export interface MeetingGateway {
  getMeetingQuota(signal?: AbortSignal): Promise<MeetingQuota>;
  createMeeting(input: CreateMeetingInput): Promise<MeetingSession>;
  stopMeeting(meetingId: string, idempotencyKey: string): Promise<MeetingResult>;
  getMeeting(meetingId: string, signal?: AbortSignal): Promise<MeetingResult>;
  listTranscriptSegments(meetingId: string, signal?: AbortSignal): Promise<TranscriptSegment[]>;
  getMeetingSummary(meetingId: string, signal?: AbortSignal): Promise<MeetingSummaryResult>;
  regenerateMeetingSummary(meetingId: string, idempotencyKey: string): Promise<MeetingSummaryResult>;
}

export class HttpMeetingGateway implements MeetingGateway {
  constructor(private readonly client: ApiClient) {}

  async getMeetingQuota(signal?: AbortSignal): Promise<MeetingQuota> {
    const rawResponse = await this.client.request("/v1/meeting-quota", { signal });
    return parseMeetingQuotaResponse(rawResponse);
  }

  async createMeeting(input: CreateMeetingInput): Promise<MeetingSession> {
    const rawResponse = await this.client.request("/v1/meetings", {
      method: "POST",
      headers: { "Idempotency-Key": input.idempotencyKey },
      body: JSON.stringify({
        language: meetingLanguageToProto(input.language),
        retain_audio: input.retainAudio,
        transcription_consent: input.transcriptionConsent,
      }),
    });
    const response = parseCreateMeetingResponse(rawResponse);
    return {
      meetingId: response.meeting.meetingId,
      status: parseMeetingStatus(response.meeting.status),
      startedAt: response.meeting.startedAt,
      websocketUrl: response.transcriptionSession.websocketUrl,
      sessionTicket: response.transcriptionSession.sessionTicket,
      audio: {
        mimeType: response.transcriptionSession.audio.mimeType,
        sampleRate: response.transcriptionSession.audio.sampleRate,
        channels: response.transcriptionSession.audio.channels,
        chunkDurationMs: response.transcriptionSession.audio.chunkDurationMs,
      },
    };
  }

  async stopMeeting(meetingId: string, idempotencyKey: string): Promise<MeetingResult> {
    const rawResponse = await this.client.request(
      `/v1/meetings/${encodeURIComponent(meetingId)}:stop`,
      {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey },
        body: JSON.stringify({}),
      },
    );
    const response = parseStopMeetingResponse(rawResponse);
    return {
      meetingId: response.meeting.meetingId,
      status: parseMeetingStatus(response.meeting.status),
      summary: response.summary,
    };
  }

  async getMeeting(meetingId: string, signal?: AbortSignal): Promise<MeetingResult> {
    const rawResponse = await this.client.request(
      `/v1/meetings/${encodeURIComponent(meetingId)}`,
      { signal },
    );
    const response = parseGetMeetingResponse(rawResponse);
    return {
      meetingId: response.meeting.meetingId,
      status: parseMeetingStatus(response.meeting.status),
      summaryStatus: parseMeetingSummaryStatus(response.meeting.summaryStatus),
    };
  }

  async getMeetingSummary(meetingId: string, signal?: AbortSignal): Promise<MeetingSummaryResult> {
    const rawResponse = await this.client.request(
      `/v1/meetings/${encodeURIComponent(meetingId)}/summary`,
      { signal },
    );
    return parseMeetingSummaryResponse(rawResponse);
  }

  async regenerateMeetingSummary(
    meetingId: string,
    idempotencyKey: string,
  ): Promise<MeetingSummaryResult> {
    const rawResponse = await this.client.request(
      `/v1/meetings/${encodeURIComponent(meetingId)}/summary:regenerate`,
      {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey },
        body: JSON.stringify({}),
      },
    );
    if (!isRecord(rawResponse)) {
      throw new Error("Regenerate meeting summary response is invalid");
    }
    return {
      status: parseMeetingSummaryStatus(readField(rawResponse, "status", "status")),
    };
  }

  async listTranscriptSegments(
    meetingId: string,
    signal?: AbortSignal,
  ): Promise<TranscriptSegment[]> {
    const segments: TranscriptSegment[] = [];
    let pageToken = "";
    for (let page = 0; page < 50; page += 1) {
      const query = new URLSearchParams({ page_size: "200" });
      if (pageToken) {
        query.set("page_token", pageToken);
      }
      const rawResponse = await this.client.request(
        `/v1/meetings/${encodeURIComponent(meetingId)}/transcript-segments?${query.toString()}`,
        { signal },
      );
      const response = parseTranscriptSegmentsResponse(rawResponse);
      segments.push(...response.segments);
      if (!response.nextPageToken) {
        return segments;
      }
      if (response.nextPageToken === pageToken) {
        throw new Error("Transcript pagination token did not advance");
      }
      pageToken = response.nextPageToken;
    }
    throw new Error("会议转写片段超过 10000 条客户端加载上限");
  }

}

export class MockMeetingGateway implements MeetingGateway {
  async getMeetingQuota(_signal?: AbortSignal): Promise<MeetingQuota> {
    return {
      periodStart: new Date(Date.UTC(2026, 7, 31, 16)).toISOString(),
      periodEnd: new Date(Date.UTC(2026, 8, 30, 16)).toISOString(),
      baseLimitSeconds: 7_200,
      purchasedLimitSeconds: 1_800,
      totalLimitSeconds: 9_000,
      consumedSeconds: 1_200,
      reservedSeconds: 0,
      remainingSeconds: 7_800,
      maxMeetingSeconds: 14_400,
      maxConcurrentMeetings: 1,
      activeMeetings: 0,
    };
  }

  async createMeeting(_input: CreateMeetingInput): Promise<MeetingSession> {
    return {
      meetingId: crypto.randomUUID(),
      status: "recording",
      startedAt: new Date().toISOString(),
      audio: {
        mimeType: "audio/pcm;rate=16000",
        sampleRate: 16_000,
        channels: 1,
        chunkDurationMs: 200,
      },
    };
  }

  async stopMeeting(meetingId: string, _idempotencyKey: string): Promise<MeetingResult> {
    await new Promise((resolve) => window.setTimeout(resolve, 700));
    return {
      meetingId,
      status: "completed",
      summary: mockSummary,
    };
  }

  async getMeeting(meetingId: string, _signal?: AbortSignal): Promise<MeetingResult> {
    return { meetingId, status: "completed", summary: mockSummary };
  }

  async listTranscriptSegments(
    _meetingId: string,
    _signal?: AbortSignal,
  ): Promise<TranscriptSegment[]> {
    return mockTranscript;
  }

  async getMeetingSummary(
    _meetingId: string,
    _signal?: AbortSignal,
  ): Promise<MeetingSummaryResult> {
    return { status: "succeeded", summary: mockSummary };
  }

  async regenerateMeetingSummary(
    _meetingId: string,
    _idempotencyKey: string,
  ): Promise<MeetingSummaryResult> {
    return { status: "processing" };
  }
}

function parseMeetingStatus(raw: unknown): MeetingStatus {
  switch (raw) {
    case 1:
    case "MEETING_STATUS_RECORDING":
      return "recording";
    case 2:
    case "MEETING_STATUS_PROCESSING":
      return "processing";
    case 3:
    case "MEETING_STATUS_COMPLETED":
      return "completed";
    case 4:
    case "MEETING_STATUS_PARTIALLY_COMPLETED":
      return "partially_completed";
    case 5:
    case "MEETING_STATUS_FAILED":
      return "failed";
    case 6:
    case "MEETING_STATUS_CANCELLED":
      return "cancelled";
    default:
      throw new Error(`Unknown meeting status: ${raw}`);
  }
}

function parseMeetingSummaryStatus(raw: unknown): MeetingSummaryResult["status"] {
  switch (raw) {
    case 1:
    case "MEETING_SUMMARY_STATUS_NOT_STARTED":
      return "not_started";
    case 2:
    case "MEETING_SUMMARY_STATUS_PENDING":
      return "pending";
    case 3:
    case "MEETING_SUMMARY_STATUS_PROCESSING":
      return "processing";
    case 4:
    case "MEETING_SUMMARY_STATUS_SUCCEEDED":
      return "succeeded";
    case 5:
    case "MEETING_SUMMARY_STATUS_FAILED":
      return "failed";
    default:
      throw new Error(`Unknown meeting summary status: ${raw}`);
  }
}

function parseMeetingQuotaResponse(value: unknown): MeetingQuota {
  if (!isRecord(value) || !isRecord(value.quota)) {
    throw new Error("Meeting quota response is invalid");
  }
  const quota = value.quota;
  const legacyLimit = parseProtoNonNegativeDurationSeconds(
    readField(quota, "limit", "limit"),
    "quota.limit",
  );
  const baseLimitSeconds = optionalProtoDurationSeconds(
    readField(quota, "baseLimit", "base_limit"),
    "quota.baseLimit",
  ) ?? legacyLimit;
  const purchasedLimitSeconds = optionalProtoDurationSeconds(
    readField(quota, "purchasedLimit", "purchased_limit"),
    "quota.purchasedLimit",
  ) ?? 0;
  const totalLimitSeconds = optionalProtoDurationSeconds(
    readField(quota, "totalLimit", "total_limit"),
    "quota.totalLimit",
  ) ?? legacyLimit;
  if (baseLimitSeconds + purchasedLimitSeconds !== totalLimitSeconds) {
    throw new Error("Meeting quota limits are inconsistent");
  }
  const consumedSeconds = parseProtoNonNegativeDurationSeconds(
    readField(quota, "consumed", "consumed"),
    "quota.consumed",
  );
  const reservedSeconds = parseProtoNonNegativeDurationSeconds(
    readField(quota, "reserved", "reserved"),
    "quota.reserved",
  );
  const remainingSeconds = parseProtoNonNegativeDurationSeconds(
    readField(quota, "remaining", "remaining"),
    "quota.remaining",
  );
  if (consumedSeconds + reservedSeconds + remainingSeconds !== totalLimitSeconds) {
    throw new Error("Meeting quota balances are inconsistent");
  }
  const periodStart = parseProtoTimestamp(
    readField(quota, "periodStart", "period_start"),
    "quota.periodStart",
  );
  const periodEnd = parseProtoTimestamp(
    readField(quota, "periodEnd", "period_end"),
    "quota.periodEnd",
  );
  if (Date.parse(periodEnd) <= Date.parse(periodStart)) {
    throw new Error("Meeting quota period is invalid");
  }
  const maxMeetingSeconds = parseProtoNonNegativeDurationSeconds(
    readField(quota, "maxMeetingDuration", "max_meeting_duration"),
    "quota.maxMeetingDuration",
  );
  const maxConcurrentMeetings = parseProtoInteger(
    readField(quota, "maxConcurrentMeetings", "max_concurrent_meetings"),
    "quota.maxConcurrentMeetings",
  );
  const activeMeetings = parseProtoInteger(
    readField(quota, "activeMeetings", "active_meetings") ?? 0,
    "quota.activeMeetings",
  );
  if (maxMeetingSeconds <= 0 || maxConcurrentMeetings <= 0 || activeMeetings < 0) {
    throw new Error("Meeting quota limits are invalid");
  }
  return {
    periodStart,
    periodEnd,
    baseLimitSeconds,
    purchasedLimitSeconds,
    totalLimitSeconds,
    consumedSeconds,
    reservedSeconds,
    remainingSeconds,
    maxMeetingSeconds,
    maxConcurrentMeetings,
    activeMeetings,
  };
}

function parseCreateMeetingResponse(value: unknown): {
  meeting: { meetingId: string; status: unknown; startedAt: string };
  transcriptionSession: {
    websocketUrl?: string;
    sessionTicket?: string;
    audio: {
      mimeType: string;
      sampleRate?: number;
      channels: number;
      chunkDurationMs: number;
    };
  };
} {
  if (!isRecord(value) || !isRecord(value.meeting)) {
    throw new Error("Create meeting response is invalid");
  }
  const meeting = value.meeting;
  const transcription = readRecord(value, "transcriptionSession", "transcription_session");
  if (!transcription) {
    throw new Error("Create meeting response is missing a transcription session");
  }
  if (!isRecord(transcription.audio)) {
    throw new Error("Create meeting response is missing audio constraints");
  }
  const audio = transcription.audio;
  const meetingId = requireString(readField(meeting, "meetingId", "meeting_id"), "meeting.meetingId");
  const status = readField(meeting, "status", "status");
  parseMeetingStatus(status);
  const startedAt = parseProtoTimestamp(readField(meeting, "startedAt", "started_at"), "meeting.startedAt");
  const mimeType = requireString(readField(audio, "mimeType", "mime_type"), "transcriptionSession.audio.mimeType");
  const channels = requireNumber(audio.channels, "transcriptionSession.audio.channels");
  const chunkDurationMs = parseProtoDurationMilliseconds(
    readField(audio, "chunkDuration", "chunk_duration"),
    "transcriptionSession.audio.chunkDuration",
  );
  const sampleRate = optionalNumber(readField(audio, "sampleRate", "sample_rate"), "transcriptionSession.audio.sampleRate");
  const websocketUrl = optionalString(
    readField(transcription, "websocketUrl", "websocket_url"),
    "transcriptionSession.websocketUrl",
  );
  const sessionTicket = optionalString(
    readField(transcription, "sessionTicket", "session_ticket"),
    "transcriptionSession.sessionTicket",
  );
  return {
    meeting: { meetingId, status, startedAt },
    transcriptionSession: {
      websocketUrl,
      sessionTicket,
      audio: {
        mimeType,
        sampleRate,
        channels,
        chunkDurationMs,
      },
    },
  };
}

function parseStopMeetingResponse(value: unknown): {
  meeting: { meetingId: string; status: unknown };
  summary?: MeetingSummary;
} {
  if (!isRecord(value) || !isRecord(value.meeting)) {
    throw new Error("Stop meeting response is invalid");
  }
  return {
    meeting: {
      meetingId: requireString(readField(value.meeting, "meetingId", "meeting_id"), "meeting.meetingId"),
      status: readField(value.meeting, "status", "status"),
    },
    summary: value.summary === undefined ? undefined : parseSummary(value.summary),
  };
}

function parseGetMeetingResponse(value: unknown): {
  meeting: { meetingId: string; status: unknown; summaryStatus: unknown };
} {
  if (!isRecord(value) || !isRecord(value.meeting)) {
    throw new Error("Get meeting response is invalid");
  }
  const meetingId = requireString(
    readField(value.meeting, "meetingId", "meeting_id"),
    "meeting.meetingId",
  );
  const status = readField(value.meeting, "status", "status");
  parseMeetingStatus(status);
  const summaryStatus = readField(value.meeting, "summaryStatus", "summary_status") ?? 1;
  parseMeetingSummaryStatus(summaryStatus);
  return { meeting: { meetingId, status, summaryStatus } };
}

function parseMeetingSummaryResponse(value: unknown): MeetingSummaryResult {
  if (!isRecord(value)) {
    throw new Error("Meeting summary response is invalid");
  }
  const status = parseMeetingSummaryStatus(readField(value, "status", "status"));
  const rawSummary = readField(value, "summary", "summary");
  return {
    status,
    summary: rawSummary === undefined || rawSummary === null ? undefined : parseSummary(rawSummary),
    failureReason: optionalString(
      readField(value, "failureReason", "failure_reason"),
      "failureReason",
    ),
  };
}

function parseTranscriptSegmentsResponse(value: unknown): {
  segments: TranscriptSegment[];
  nextPageToken: string;
} {
  if (!isRecord(value)) {
    throw new Error("Transcript segments response is invalid");
  }
  const rawSegments = value.segments ?? [];
  if (!Array.isArray(rawSegments)) {
    throw new Error("Transcript segments response must contain an array");
  }
  const nextPageToken = optionalString(
    readField(value, "nextPageToken", "next_page_token"),
    "nextPageToken",
  );
  return {
    segments: rawSegments.map((segment, index) => parseTranscriptSegment(segment, index)),
    nextPageToken: nextPageToken ?? "",
  };
}

function parseTranscriptSegment(value: unknown, index: number): TranscriptSegment {
  if (!isRecord(value)) {
    throw new Error(`segments[${index}] must be an object`);
  }
  const sequenceNo = parseProtoInteger(
    readField(value, "sequenceNo", "sequence_no"),
    `segments[${index}].sequenceNo`,
  );
  const startOffsetMs = parseProtoNonNegativeDurationMilliseconds(
    readField(value, "startOffset", "start_offset"),
    `segments[${index}].startOffset`,
  );
  const endOffsetMs = parseProtoNonNegativeDurationMilliseconds(
    readField(value, "endOffset", "end_offset"),
    `segments[${index}].endOffset`,
  );
  if (sequenceNo <= 0 || endOffsetMs < startOffsetMs) {
    throw new Error(`segments[${index}] has an invalid sequence or offset range`);
  }
  return {
    id: requireString(readField(value, "segmentId", "segment_id"), `segments[${index}].segmentId`),
    sequenceNo,
    startOffsetMs,
    endOffsetMs,
    speakerLabel: optionalString(
      readField(value, "speakerLabel", "speaker_label"),
      `segments[${index}].speakerLabel`,
    ),
    content: requireString(value.content, `segments[${index}].content`),
    isFinal: true,
  };
}

function parseSummary(value: unknown): MeetingSummary {
  if (!isRecord(value)) {
    throw new Error("Meeting summary is invalid");
  }
  // Proto JSON omits empty repeated fields by default. Treat omission as an
  // empty collection while still rejecting a present value with the wrong type.
  const rawActionItems = readField(value, "actionItems", "action_items") ?? [];
  if (!Array.isArray(rawActionItems)) {
    throw new Error("Meeting summary actionItems must be an array");
  }
  return {
    meetingId: optionalString(readField(value, "meetingId", "meeting_id"), "summary.meetingId"),
    version: optionalProtoInteger(readField(value, "version", "version"), "summary.version"),
    sourceTranscriptRevision: optionalProtoInteger(
      readField(value, "sourceTranscriptRevision", "source_transcript_revision"),
      "summary.sourceTranscriptRevision",
    ),
    topic: requireString(value.topic, "summary.topic"),
    abstract: requireString(value.abstract, "summary.abstract"),
    keyDiscussions: requireStringArray(
      readField(value, "keyDiscussions", "key_discussions") ?? [],
      "summary.keyDiscussions",
    ),
    decisions: requireStringArray(value.decisions ?? [], "summary.decisions"),
    actionItems: rawActionItems.map((item, index) => {
      if (!isRecord(item)) {
        throw new Error(`summary.actionItems[${index}] must be an object`);
      }
      return {
        assignee: optionalString(item.assignee, `summary.actionItems[${index}].assignee`),
        task: requireString(item.task, `summary.actionItems[${index}].task`),
        dueText: optionalString(
          readField(item, "dueText", "due_text"),
          `summary.actionItems[${index}].dueText`,
        ),
      };
    }),
    risks: requireStringArray(value.risks ?? [], "summary.risks"),
    generatedAt: optionalProtoTimestamp(
      readField(value, "generatedAt", "generated_at"),
      "summary.generatedAt",
    ),
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function readField(value: Record<string, unknown>, camel: string, snake: string): unknown {
  return value[camel] ?? value[snake];
}

function readRecord(
  value: Record<string, unknown>,
  camel: string,
  snake: string,
): Record<string, unknown> | undefined {
  const field = readField(value, camel, snake);
  return isRecord(field) ? field : undefined;
}

function requireString(value: unknown, field: string): string {
  if (typeof value !== "string" || !value) {
    throw new Error(`${field} must be a non-empty string`);
  }
  return value;
}

function optionalString(value: unknown, field: string): string | undefined {
  if (value === undefined || value === null || value === "") {
    return undefined;
  }
  if (typeof value !== "string") {
    throw new Error(`${field} must be a string`);
  }
  return value;
}

function requireNumber(value: unknown, field: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new Error(`${field} must be a finite number`);
  }
  return value;
}

function optionalNumber(value: unknown, field: string): number | undefined {
  if (value === undefined || value === null) {
    return undefined;
  }
  return requireNumber(value, field);
}

function optionalProtoInteger(value: unknown, field: string): number | undefined {
  if (value === undefined || value === null || value === "") {
    return undefined;
  }
  return parseProtoInteger(value, field);
}

function optionalProtoTimestamp(value: unknown, field: string): string | undefined {
  if (value === undefined || value === null || value === "") {
    return undefined;
  }
  return parseProtoTimestamp(value, field);
}

function meetingLanguageToProto(language: CreateMeetingInput["language"]): number {
  switch (language) {
    case "auto":
      return 1;
    case "zh-CN":
      return 2;
    case "en-US":
      return 3;
  }
}

function parseProtoDurationMilliseconds(value: unknown, field: string): number {
  if (isRecord(value)) {
    const seconds = parseProtoInteger(value.seconds ?? 0, `${field}.seconds`);
    const nanoseconds = parseProtoInteger(value.nanos ?? 0, `${field}.nanos`);
    const milliseconds = seconds * 1_000 + nanoseconds / 1_000_000;
    if (!Number.isSafeInteger(milliseconds) || milliseconds <= 0) {
      throw new Error(`${field} must resolve to positive whole milliseconds`);
    }
    return milliseconds;
  }
  if (typeof value !== "string") {
    throw new Error(`${field} must be a protobuf duration`);
  }
  const match = /^(0|[1-9][0-9]{0,11})(?:\.([0-9]{1,9}))?s$/.exec(value);
  if (!match) {
    throw new Error(`${field} must be a positive protobuf duration`);
  }
  const secondsRaw = match[1];
  if (secondsRaw === undefined) {
    throw new Error(`${field} is invalid`);
  }
  const seconds = Number.parseInt(secondsRaw, 10);
  const fraction = (match[2] ?? "").padEnd(9, "0");
  const nanoseconds = fraction === "" ? 0 : Number.parseInt(fraction, 10);
  const milliseconds = seconds * 1_000 + nanoseconds / 1_000_000;
  if (!Number.isSafeInteger(milliseconds) || milliseconds <= 0) {
    throw new Error(`${field} must resolve to positive whole milliseconds`);
  }
  return milliseconds;
}

function parseProtoNonNegativeDurationMilliseconds(value: unknown, field: string): number {
  if (isRecord(value)) {
    const seconds = parseProtoInteger(value.seconds ?? 0, `${field}.seconds`);
    const nanoseconds = parseProtoInteger(value.nanos ?? 0, `${field}.nanos`);
    const milliseconds = seconds * 1_000 + nanoseconds / 1_000_000;
    if (!Number.isSafeInteger(milliseconds) || milliseconds < 0) {
      throw new Error(`${field} must resolve to non-negative whole milliseconds`);
    }
    return milliseconds;
  }
  if (typeof value !== "string") {
    throw new Error(`${field} must be a protobuf duration`);
  }
  const match = /^(0|[1-9][0-9]{0,11})(?:\.([0-9]{1,9}))?s$/.exec(value);
  if (!match) {
    throw new Error(`${field} must be a non-negative protobuf duration`);
  }
  const secondsRaw = match[1];
  if (secondsRaw === undefined) {
    throw new Error(`${field} is invalid`);
  }
  const seconds = Number.parseInt(secondsRaw, 10);
  const fraction = (match[2] ?? "").padEnd(9, "0");
  const nanoseconds = fraction === "" ? 0 : Number.parseInt(fraction, 10);
  const milliseconds = seconds * 1_000 + nanoseconds / 1_000_000;
  if (!Number.isSafeInteger(milliseconds) || milliseconds < 0) {
    throw new Error(`${field} must resolve to non-negative whole milliseconds`);
  }
  return milliseconds;
}

function parseProtoNonNegativeDurationSeconds(value: unknown, field: string): number {
  const milliseconds = parseProtoNonNegativeDurationMilliseconds(value, field);
  const seconds = milliseconds / 1_000;
  if (!Number.isSafeInteger(seconds)) {
    throw new Error(`${field} must resolve to whole seconds`);
  }
  return seconds;
}

function optionalProtoDurationSeconds(value: unknown, field: string): number | undefined {
  if (value === undefined || value === null || value === "") {
    return undefined;
  }
  return parseProtoNonNegativeDurationSeconds(value, field);
}

function parseProtoTimestamp(value: unknown, field: string): string {
  if (typeof value === "string" && !Number.isNaN(Date.parse(value))) {
    return new Date(value).toISOString();
  }
  if (!isRecord(value)) {
    throw new Error(`${field} must be a protobuf timestamp`);
  }
  const seconds = parseProtoInteger(value.seconds ?? 0, `${field}.seconds`);
  const nanoseconds = parseProtoInteger(value.nanos ?? 0, `${field}.nanos`);
  const milliseconds = seconds * 1_000 + Math.floor(nanoseconds / 1_000_000);
  if (!Number.isSafeInteger(milliseconds)) {
    throw new Error(`${field} is out of range`);
  }
  const timestamp = new Date(milliseconds);
  if (Number.isNaN(timestamp.getTime())) {
    throw new Error(`${field} is invalid`);
  }
  return timestamp.toISOString();
}

function parseProtoInteger(value: unknown, field: string): number {
  const parsed = typeof value === "string" ? Number(value) : value;
  if (typeof parsed !== "number" || !Number.isSafeInteger(parsed)) {
    throw new Error(`${field} must be an integer`);
  }
  return parsed;
}

function requireStringArray(value: unknown, field: string): string[] {
  if (!Array.isArray(value) || !value.every((item) => typeof item === "string")) {
    throw new Error(`${field} must be a string array`);
  }
  return value;
}

const mockSummary: MeetingSummary = {
  topic: "AI 会议助手 MVP 讨论",
  abstract: "团队确认优先完成实时转写、结构化纪要和 Markdown 导出。",
  keyDiscussions: ["uTools 插件采用轻量 Web 技术栈", "后端使用异步任务生成纪要"],
  decisions: ["MVP 首期支持 macOS", "屏幕录制延后到 V1.1"],
  actionItems: [
    { assignee: "张三", task: "完成插件录音适配", dueText: "本周五" },
    { assignee: "李四", task: "完成会议 API", dueText: "下周一" },
  ],
  risks: ["系统音频权限需要在真实 macOS 环境验证"],
};

const mockTranscript: TranscriptSegment[] = [
  {
    id: "mock-history-1",
    sequenceNo: 1,
    startOffsetMs: 0,
    endOffsetMs: 2_000,
    speakerLabel: "speaker_1",
    content: "我们先确认 AI 会议助手的 MVP 范围。",
    isFinal: true,
  },
  {
    id: "mock-history-2",
    sequenceNo: 2,
    startOffsetMs: 2_100,
    endOffsetMs: 4_000,
    speakerLabel: "speaker_2",
    content: "首期完成实时转写和 Markdown 纪要导出。",
    isFinal: true,
  },
];

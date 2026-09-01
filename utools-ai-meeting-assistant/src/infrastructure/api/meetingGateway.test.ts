import { describe, expect, it } from "vitest";

import { ApiClient } from "./apiClient";
import { HttpMeetingGateway } from "./meetingGateway";

class StubApiClient extends ApiClient {
  readonly requests: { path: string; init: RequestInit }[] = [];

  constructor(private readonly response: unknown) {
    super("http://127.0.0.1:8000");
  }

  override async request(path: string, init: RequestInit = {}): Promise<unknown> {
    this.requests.push({ path, init });
    return this.response;
  }
}

describe("HttpMeetingGateway", () => {
  it("loads the monthly quota breakdown", async () => {
    const api = new StubApiClient({
      quota: {
        period_start: { seconds: "1785513600", nanos: 0 },
        period_end: "2026-09-30T16:00:00Z",
        limit: "9000s",
        base_limit: "7200s",
        purchased_limit: { seconds: "1800", nanos: 0 },
        total_limit: "9000s",
        consumed: "1200s",
        reserved: "600s",
        remaining: "7200s",
        max_meeting_duration: "14400s",
        max_concurrent_meetings: 1,
        active_meetings: 1,
      },
    });

    const result = await new HttpMeetingGateway(api).getMeetingQuota();

    expect(result).toMatchObject({
      baseLimitSeconds: 7_200,
      purchasedLimitSeconds: 1_800,
      totalLimitSeconds: 9_000,
      consumedSeconds: 1_200,
      reservedSeconds: 600,
      remainingSeconds: 7_200,
      maxMeetingSeconds: 14_400,
      maxConcurrentMeetings: 1,
      activeMeetings: 1,
    });
    expect(api.requests[0]?.path).toBe("/v1/meeting-quota");
  });

  it("maps the legacy quota limit when breakdown fields are absent", async () => {
    const api = new StubApiClient({
      quota: {
        periodStart: "2026-08-31T16:00:00Z",
        periodEnd: "2026-09-30T16:00:00Z",
        limit: "7200s",
        consumed: "0s",
        reserved: "0s",
        remaining: "7200s",
        maxMeetingDuration: "14400s",
        maxConcurrentMeetings: 1,
        activeMeetings: 0,
      },
    });

    await expect(new HttpMeetingGateway(api).getMeetingQuota()).resolves.toMatchObject({
      baseLimitSeconds: 7_200,
      purchasedLimitSeconds: 0,
      totalLimitSeconds: 7_200,
    });
  });

  it("defaults an omitted proto3 active meeting counter to zero", async () => {
    const api = new StubApiClient({
      quota: {
        period_start: { seconds: "1788192000" },
        period_end: { seconds: "1790784000" },
        limit: { seconds: "7200" },
        consumed: {},
        reserved: {},
        remaining: { seconds: "7200" },
        max_meeting_duration: { seconds: "14400" },
        max_concurrent_meetings: 1,
        base_limit: { seconds: "7200" },
        purchased_limit: {},
        total_limit: { seconds: "7200" },
      },
    });

    await expect(new HttpMeetingGateway(api).getMeetingQuota()).resolves.toMatchObject({
      activeMeetings: 0,
      consumedSeconds: 0,
      reservedSeconds: 0,
      purchasedLimitSeconds: 0,
      remainingSeconds: 7_200,
    });
  });

  it("uses Proto JSON and maps the backend PCM contract", async () => {
    const api = new StubApiClient({
      meeting: {
        meetingId: "meeting-id",
        status: "MEETING_STATUS_RECORDING",
        startedAt: "2026-08-28T10:00:00Z",
      },
      transcriptionSession: {
        websocketUrl: "ws://127.0.0.1:8100/v1/realtime/transcriptions",
        sessionTicket: "one-time-ticket",
        audio: {
          format: "AUDIO_FORMAT_PCM_S16LE",
          mimeType: "audio/pcm;rate=16000",
          sampleRate: 16_000,
          channels: 1,
          chunkDuration: "0.200s",
          maxChunkBytes: 6_400,
        },
      },
    });
    const result = await new HttpMeetingGateway(api).createMeeting({
      language: "auto",
      retainAudio: false,
      transcriptionConsent: true,
      idempotencyKey: "idempotency-key",
    });
    expect(result).toMatchObject({
      meetingId: "meeting-id",
      status: "recording",
      websocketUrl: "ws://127.0.0.1:8100/v1/realtime/transcriptions",
      sessionTicket: "one-time-ticket",
      audio: {
        mimeType: "audio/pcm;rate=16000",
        sampleRate: 16_000,
        channels: 1,
        chunkDurationMs: 200,
      },
    });
    expect(api.requests[0]?.path).toBe("/v1/meetings");
    expect(JSON.parse(String(api.requests[0]?.init.body))).toEqual({
      language: 1,
      retain_audio: false,
      transcription_consent: true,
    });
  });

  it("maps the current snake_case and numeric backend response", async () => {
    const api = new StubApiClient({
      meeting: {
        meeting_id: "meeting-id",
        status: 1,
        started_at: { seconds: 1_787_968_979, nanos: 701_000_000 },
      },
      transcription_session: {
        websocket_url: "ws://127.0.0.1:8100/v1/realtime/transcriptions",
        session_ticket: "one-time-ticket",
        audio: {
          mime_type: "audio/pcm;rate=16000",
          sample_rate: 16_000,
          channels: 1,
          chunk_duration: { nanos: 200_000_000 },
        },
      },
    });
    const result = await new HttpMeetingGateway(api).createMeeting({
      language: "auto",
      retainAudio: false,
      transcriptionConsent: true,
      idempotencyKey: "idempotency-key",
    });
    expect(result).toMatchObject({
      meetingId: "meeting-id",
      status: "recording",
      websocketUrl: "ws://127.0.0.1:8100/v1/realtime/transcriptions",
      sessionTicket: "one-time-ticket",
      audio: { chunkDurationMs: 200 },
    });
  });

  it("queries the latest meeting status used by completion polling", async () => {
    const api = new StubApiClient({
      meeting: {
        meeting_id: "meeting-id",
        status: 3,
        transcription_status: 5,
      },
    });

    const result = await new HttpMeetingGateway(api).getMeeting("meeting/id");

    expect(result).toEqual({
      meetingId: "meeting-id",
      status: "completed",
      summaryStatus: "not_started",
    });
    expect(api.requests[0]?.path).toBe("/v1/meetings/meeting%2Fid");
    expect(api.requests[0]?.init.method).toBeUndefined();
  });

  it("loads and maps the final transcript for a local recording", async () => {
    const api = new StubApiClient({
      segments: [
        {
          segment_id: "segment-id",
          sequence_no: "1",
          start_offset: { seconds: "0", nanos: 0 },
          end_offset: "2.500s",
          speaker_label: "speaker_1",
          content: "会议最终转写",
        },
      ],
      next_page_token: "",
    });

    const result = await new HttpMeetingGateway(api).listTranscriptSegments("meeting/id");

    expect(result).toEqual([
      {
        id: "segment-id",
        sequenceNo: 1,
        startOffsetMs: 0,
        endOffsetMs: 2_500,
        speakerLabel: "speaker_1",
        content: "会议最终转写",
        isFinal: true,
      },
    ]);
    expect(api.requests[0]?.path).toBe(
      "/v1/meetings/meeting%2Fid/transcript-segments?page_size=200",
    );
  });

  it("treats an omitted repeated transcript field as an empty list", async () => {
    const api = new StubApiClient({});

    await expect(new HttpMeetingGateway(api).listTranscriptSegments("meeting-id")).resolves.toEqual(
      [],
    );
  });

  it("loads a structured meeting summary from Proto JSON", async () => {
    const api = new StubApiClient({
      status: "MEETING_SUMMARY_STATUS_SUCCEEDED",
      summary: {
        meeting_id: "meeting-id",
        version: "2",
        source_transcript_revision: "8",
        topic: "迭代计划",
        abstract: "团队确认了下一阶段安排。",
        key_discussions: ["会议总结页面"],
        decisions: ["先接入 DeepSeek"],
        action_items: [
          { assignee: "张三", task: "完成联调", due_text: "周五", status: 1 },
        ],
        risks: ["Provider 暂时不可用"],
        provider: "deepseek",
        model_name: "deepseek-v4-flash",
        prompt_version: "meeting-summary-v1",
        generated_at: "2026-08-30T05:00:00Z",
      },
    });

    const result = await new HttpMeetingGateway(api).getMeetingSummary("meeting/id");

    expect(result).toMatchObject({
      status: "succeeded",
      summary: {
        meetingId: "meeting-id",
        version: 2,
        topic: "迭代计划",
        actionItems: [{ assignee: "张三", task: "完成联调", dueText: "周五" }],
        modelName: "deepseek-v4-flash",
      },
    });
    expect(api.requests[0]?.path).toBe("/v1/meetings/meeting%2Fid/summary");
  });

  it("treats omitted repeated summary fields as empty lists", async () => {
    const api = new StubApiClient({
      status: "MEETING_SUMMARY_STATUS_SUCCEEDED",
      summary: {
        topic: "有效内容不足",
        abstract: "当前会议没有可提取的讨论项。",
      },
    });

    await expect(new HttpMeetingGateway(api).getMeetingSummary("meeting-id")).resolves.toEqual({
      status: "succeeded",
      summary: {
        topic: "有效内容不足",
        abstract: "当前会议没有可提取的讨论项。",
        keyDiscussions: [],
        decisions: [],
        actionItems: [],
        risks: [],
      },
    });
  });
});

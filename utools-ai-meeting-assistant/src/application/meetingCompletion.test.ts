import { describe, expect, it, vi } from "vitest";

import type { MeetingResult } from "@/domain/meeting";
import type { MeetingGateway } from "@/infrastructure/api/meetingGateway";
import { ApiError } from "@/infrastructure/api/apiClient";

import { MeetingCompletionError, waitForMeetingCompletion } from "./meetingCompletion";

class CompletionGateway implements Pick<MeetingGateway, "getMeeting"> {
  readonly getMeeting = vi.fn<(meetingId: string) => Promise<MeetingResult>>();
}

describe("waitForMeetingCompletion", () => {
  it("polls processing meetings until the backend reaches completed", async () => {
    const gateway = new CompletionGateway();
    gateway.getMeeting
      .mockResolvedValueOnce({ meetingId: "meeting-id", status: "processing" })
      .mockResolvedValueOnce({ meetingId: "meeting-id", status: "completed" });

    const result = await waitForMeetingCompletion(
      gateway,
      { meetingId: "meeting-id", status: "processing" },
      { sleep: async () => undefined },
    );

    expect(result.status).toBe("completed");
    expect(gateway.getMeeting).toHaveBeenCalledTimes(2);
  });

  it("cancels waiting when backend processing fails", async () => {
    const gateway = new CompletionGateway();
    gateway.getMeeting.mockResolvedValue({ meetingId: "meeting-id", status: "failed" });

    await expect(
      waitForMeetingCompletion(gateway, { meetingId: "meeting-id", status: "processing" }),
    ).rejects.toThrow(MeetingCompletionError);
  });

  it("does not poll an already completed stop response", async () => {
    const gateway = new CompletionGateway();
    const result = await waitForMeetingCompletion(gateway, {
      meetingId: "meeting-id",
      status: "completed",
    });

    expect(result.status).toBe("completed");
    expect(gateway.getMeeting).not.toHaveBeenCalled();
  });

  it("continues polling after a temporary request failure", async () => {
    const gateway = new CompletionGateway();
    gateway.getMeeting
      .mockRejectedValueOnce(new Error("temporary network failure"))
      .mockResolvedValueOnce({ meetingId: "meeting-id", status: "completed" });
    let currentTime = 0;

    const result = await waitForMeetingCompletion(
      gateway,
      { meetingId: "meeting-id", status: "processing" },
      {
        now: () => currentTime,
        sleep: async (milliseconds) => {
          currentTime += milliseconds;
        },
      },
    );

    expect(result.status).toBe("completed");
    expect(gateway.getMeeting).toHaveBeenCalledTimes(2);
  });

  it("stops immediately for a non-retryable client error", async () => {
    const gateway = new CompletionGateway();
    gateway.getMeeting.mockRejectedValue(new ApiError("meeting not found", 404, "MEETING_NOT_FOUND"));

    await expect(
      waitForMeetingCompletion(
        gateway,
        { meetingId: "meeting-id", status: "processing" },
        { sleep: async () => undefined },
      ),
    ).rejects.toThrow("查询会议处理结果失败");
    expect(gateway.getMeeting).toHaveBeenCalledTimes(1);
  });
});

import { describe, expect, it, vi } from "vitest";

import type { MeetingResult } from "@/domain/meeting";
import type { MeetingGateway } from "@/infrastructure/api/meetingGateway";

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
});

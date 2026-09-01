import { describe, expect, it } from "vitest";

import {
  canStartMeeting,
  canStopMeeting,
  formatQuotaDuration,
  getDisplayRemainingQuotaSeconds,
  isTerminalPhase,
  requiresMeetingCleanup,
} from "./meeting";

describe("meeting client state", () => {
  it("only stops an active recording", () => {
    expect(canStopMeeting("recording")).toBe(true);
    expect(canStopMeeting("processing")).toBe(false);
  });

  it("allows a new meeting from idle or a terminal state", () => {
    expect(canStartMeeting("idle")).toBe(true);
    expect(canStartMeeting("completed")).toBe(true);
    expect(canStartMeeting("recording")).toBe(false);
  });

  it("recognizes every terminal state", () => {
    expect(isTerminalPhase("partially_completed")).toBe(true);
    expect(isTerminalPhase("cancelled")).toBe(true);
    expect(isTerminalPhase("stopping")).toBe(false);
  });

  it("cleans up both startup and active capture when the plugin exits", () => {
    expect(requiresMeetingCleanup("starting")).toBe(true);
    expect(requiresMeetingCleanup("recording")).toBe(true);
    expect(requiresMeetingCleanup("stopping")).toBe(true);
    expect(requiresMeetingCleanup("processing")).toBe(false);
  });

  it("formats quota at minute precision without exposing seconds", () => {
    expect(formatQuotaDuration(0)).toBe("0 分钟");
    expect(formatQuotaDuration(59)).toBe("不足 1 分钟");
    expect(formatQuotaDuration(60)).toBe("1 分钟");
    expect(formatQuotaDuration(3_661)).toBe("1 小时 1 分钟");
  });

  it("keeps active meeting reservations in the user-facing remaining quota", () => {
    expect(getDisplayRemainingQuotaSeconds({
      periodStart: "2026-09-01T00:00:00Z",
      periodEnd: "2026-10-01T00:00:00Z",
      baseLimitSeconds: 10_800,
      purchasedLimitSeconds: 0,
      totalLimitSeconds: 10_800,
      consumedSeconds: 491,
      reservedSeconds: 10_309,
      remainingSeconds: 0,
      maxMeetingSeconds: 10_800,
      maxConcurrentMeetings: 1,
      activeMeetings: 1,
    })).toBe(10_309);
  });
});

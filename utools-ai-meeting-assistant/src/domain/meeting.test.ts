import { describe, expect, it } from "vitest";

import {
  canStartMeeting,
  canStopMeeting,
  formatQuotaDuration,
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

  it("formats quota without independently rounding each balance", () => {
    expect(formatQuotaDuration(0)).toBe("0 分钟");
    expect(formatQuotaDuration(59)).toBe("59 秒");
    expect(formatQuotaDuration(60)).toBe("1 分钟");
    expect(formatQuotaDuration(3_661)).toBe("1 小时 1 分钟 1 秒");
  });
});

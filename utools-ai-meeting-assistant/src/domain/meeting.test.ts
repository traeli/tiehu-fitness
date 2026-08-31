import { describe, expect, it } from "vitest";

import { canStartMeeting, canStopMeeting, isTerminalPhase } from "./meeting";

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
});

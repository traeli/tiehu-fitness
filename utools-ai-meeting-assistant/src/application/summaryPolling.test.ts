import { describe, expect, it } from "vitest";

import { nextSummaryPollDelay } from "./summaryPolling";

describe("nextSummaryPollDelay", () => {
  it("stops polling once the bounded tracking window expires", () => {
    expect(nextSummaryPollDelay(0, 1_000, 1_000)).toBeUndefined();
    expect(nextSummaryPollDelay(3, 1_001, 1_000)).toBeUndefined();
  });

  it("bounds retry backoff by the remaining tracking window", () => {
    expect(nextSummaryPollDelay(0, 1_000, 20_000)).toBe(2_000);
    expect(nextSummaryPollDelay(3, 1_000, 20_000)).toBe(15_000);
    expect(nextSummaryPollDelay(2, 1_000, 1_500)).toBe(500);
  });
});

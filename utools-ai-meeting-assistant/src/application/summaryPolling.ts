const maximumSummaryPollDelayMs = 15_000;
const initialSummaryPollDelayMs = 2_000;

// Returns no delay once the caller's bounded tracking window has elapsed.
// This keeps an optional summary job from leaving the history UI spinning
// forever when the server or provider cannot reach a terminal state.
export function nextSummaryPollDelay(
  failedAttempts: number,
  now: number,
  deadline: number,
): number | undefined {
  if (!Number.isSafeInteger(failedAttempts) || failedAttempts < 0) {
    throw new Error("summary polling failure count is invalid");
  }
  if (!Number.isFinite(now) || !Number.isFinite(deadline)) {
    throw new Error("summary polling deadline is invalid");
  }
  const remaining = deadline - now;
  if (remaining <= 0) {
    return undefined;
  }
  const backoff = initialSummaryPollDelayMs * 2 ** Math.min(failedAttempts, 3);
  return Math.min(remaining, maximumSummaryPollDelayMs, backoff);
}

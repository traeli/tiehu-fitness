import type { MeetingResult } from "@/domain/meeting";
import type { MeetingGateway } from "@/infrastructure/api/meetingGateway";

const defaultTimeoutMs = 30_000;
const defaultPollIntervalMs = 500;
const defaultRequestTimeoutMs = 5_000;

export class MeetingCompletionError extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "MeetingCompletionError";
  }
}

interface MeetingCompletionOptions {
  timeoutMs?: number;
  pollIntervalMs?: number;
  requestTimeoutMs?: number;
  now?: () => number;
  sleep?: (milliseconds: number) => Promise<void>;
}

export async function waitForMeetingCompletion(
  gateway: Pick<MeetingGateway, "getMeeting">,
  initial: MeetingResult,
  options: MeetingCompletionOptions = {},
): Promise<MeetingResult> {
  if (initial.status !== "processing") {
    return requireSuccessfulTerminal(initial);
  }

  const timeoutMs = positiveOption(options.timeoutMs, defaultTimeoutMs, "timeoutMs");
  const pollIntervalMs = positiveOption(
    options.pollIntervalMs,
    defaultPollIntervalMs,
    "pollIntervalMs",
  );
  const requestTimeoutMs = positiveOption(
    options.requestTimeoutMs,
    defaultRequestTimeoutMs,
    "requestTimeoutMs",
  );
  const now = options.now ?? Date.now;
  const sleep = options.sleep ?? delay;
  const deadline = now() + timeoutMs;

  while (now() < deadline) {
    const remainingMs = deadline - now();
    const signal = AbortSignal.timeout(Math.min(requestTimeoutMs, remainingMs));
    let current: MeetingResult;
    try {
      current = await gateway.getMeeting(initial.meetingId, signal);
    } catch (error) {
      throw new MeetingCompletionError("查询会议处理结果失败，已停止等待。", {
        cause: error,
      });
    }
    if (current.status !== "processing") {
      return requireSuccessfulTerminal(current);
    }
    await sleep(Math.min(pollIntervalMs, Math.max(1, deadline - now())));
  }
  throw new MeetingCompletionError("会议处理超时，已停止等待。请稍后查询会议状态。");
}

function requireSuccessfulTerminal(result: MeetingResult): MeetingResult {
  if (result.status === "failed") {
    throw new MeetingCompletionError("会议转写处理失败，已取消本次纪要生成。");
  }
  if (result.status === "recording") {
    throw new MeetingCompletionError("停止会议后服务仍返回录制状态，已停止等待。");
  }
  return result;
}

function positiveOption(value: number | undefined, fallback: number, name: string): number {
  const resolved = value ?? fallback;
  if (!Number.isFinite(resolved) || resolved <= 0) {
    throw new MeetingCompletionError(`${name} must be positive`);
  }
  return resolved;
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
}

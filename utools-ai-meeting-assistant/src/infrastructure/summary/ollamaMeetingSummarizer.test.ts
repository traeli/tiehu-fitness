import { describe, expect, it, vi } from "vitest";

import { summarizeMeeting } from "./ollamaMeetingSummarizer";

const transcript = [{
  id: "segment-1",
  sequenceNo: 1,
  startOffsetMs: 5_000,
  endOffsetMs: 10_000,
  content: "团队决定周五发布，由小王完成验收。",
  isFinal: true,
}];

describe("summarizeMeeting", () => {
  it("uses the backend-compatible prompt and maps structured Ollama JSON", async () => {
    const chat = vi.fn(async () => ({
      content: JSON.stringify({
        topic: "发布计划",
        abstract: "团队确认了发布安排。",
        key_discussions: ["周五发布"],
        decisions: ["按计划发布"],
        action_items: [{ assignee: "小王", task: "完成验收", due_text: "周五前", status: "pending" }],
        risks: [],
      }),
      inputTokens: 50,
      outputTokens: 30,
    }));

    const summary = await summarizeMeeting({ chat }, {
      provider: "ollama",
      model: "deepseek-r1:7b",
      transcript,
      customPrompt: "突出行动事项",
    });

    expect(summary.topic).toBe("发布计划");
    expect(summary.actionItems[0]).toEqual({ assignee: "小王", task: "完成验收", dueText: "周五前", status: "pending" });
    expect(summary.model).toBe("deepseek-r1:7b");
    expect(summary.provider).toBe("ollama");
    expect(summary.inputTokens).toBe(50);
    expect(chat).toHaveBeenCalledWith(expect.objectContaining({
      systemPrompt: expect.stringContaining("突出行动事项"),
      userPrompt: expect.stringContaining("<meeting_data>"),
    }));
  });

  it("rejects malformed output instead of storing an incomplete summary", async () => {
    const chat = vi.fn(async () => ({ content: "not json", inputTokens: 0, outputTokens: 0 }));
    await expect(summarizeMeeting({ chat }, {
      provider: "deepseek",
      model: "deepseek-r1:7b",
      transcript,
    })).rejects.toThrow("不是有效 JSON");
  });
});

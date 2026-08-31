import { describe, expect, it } from "vitest";

import { renderSummaryMarkdown } from "./summaryMarkdown";

describe("summary markdown", () => {
  it("renders structured content and escapes table cells", () => {
    const markdown = renderSummaryMarkdown({
      topic: "例会",
      abstract: "确认交付计划。",
      keyDiscussions: ["发布节奏"],
      decisions: ["周五发布"],
      actionItems: [{ assignee: "张三", task: "更新 A|B 文档", dueText: "周四" }],
      risks: [],
    });

    expect(markdown).toContain("# 例会");
    expect(markdown).toContain("更新 A\\|B 文档");
    expect(markdown).toContain("## 风险\n\n- 无");
  });
});

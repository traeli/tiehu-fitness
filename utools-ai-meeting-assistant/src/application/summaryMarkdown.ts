import type { MeetingSummary } from "@/domain/meeting";

export function renderSummaryMarkdown(summary: MeetingSummary): string {
  const lines = [
    `# ${summary.topic}`,
    "",
    "## 会议摘要",
    "",
    summary.abstract,
    "",
    "## 主要讨论",
    "",
    ...renderList(summary.keyDiscussions),
    "",
    "## 核心决定",
    "",
    ...renderList(summary.decisions),
    "",
    "## 待办事项",
    "",
    "| 负责人 | 任务 | 时间 |",
    "|---|---|---|",
    ...summary.actionItems.map(
      (item) => `| ${escapeCell(item.assignee ?? "待定")} | ${escapeCell(item.task)} | ${escapeCell(item.dueText ?? "待定")} |`,
    ),
    "",
    "## 风险",
    "",
    ...renderList(summary.risks),
    "",
  ];
  return lines.join("\n");
}

function renderList(values: string[]): string[] {
  return values.length > 0 ? values.map((value) => `- ${value}`) : ["- 无"];
}

function escapeCell(value: string): string {
  return value.replace(/\|/g, "\\|").replace(/\r?\n/g, " ");
}

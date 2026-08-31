package localfake

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

type Provider struct {
	model         string
	promptVersion string
}

func NewProvider(model, promptVersion string) (*Provider, error) {
	if strings.TrimSpace(model) == "" || strings.TrimSpace(promptVersion) == "" {
		return nil, fmt.Errorf("local fake LLM model and prompt version are required")
	}
	return &Provider{model: model, promptVersion: promptVersion}, nil
}

func (p *Provider) Summarize(ctx context.Context, request *biz.MeetingSummaryGenerationRequest) (*biz.MeetingSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil || request.Snapshot == nil || len(request.Snapshot.Segments) == 0 {
		return nil, fmt.Errorf("meeting transcript is empty")
	}
	snapshot := request.Snapshot
	first := strings.TrimSpace(snapshot.Segments[0].Content)
	if first == "" {
		first = "会议已完成"
	}
	if len([]rune(first)) > 120 {
		first = string([]rune(first)[:120])
	}
	return &biz.MeetingSummary{
		Topic:          "本地模拟会议总结",
		Abstract:       fmt.Sprintf("本地模拟 LLM 已读取 %d 段最终转写。首段内容：%s", len(snapshot.Segments), first),
		KeyDiscussions: []string{"验证最终转写能够提交给异步会议总结任务"},
		Decisions:      []string{"会议总结链路使用可替换的 Provider Adapter"},
		ActionItems:    []biz.MeetingActionItem{{Task: "配置真实 DeepSeek API Key 后验证总结质量", Status: "pending"}},
		Risks:          []string{"当前结果由本地模拟 Provider 生成，不代表真实模型质量"},
		InputTokens:    int64(len([]rune(first))),
		OutputTokens:   100,
		GeneratedAt:    time.Now().UTC(),
	}, nil
}

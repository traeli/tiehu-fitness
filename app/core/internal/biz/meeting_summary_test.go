package biz

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMeetingSummaryValidateCompleted(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	valid := &MeetingSummary{
		MeetingID: uuid.NewString(), Version: 1, SourceTranscriptRevision: 3,
		Status: MeetingSummaryStatusSucceeded, Topic: "迭代计划", Abstract: "团队确认了下一阶段工作。",
		KeyDiscussions: []string{"会议详情页"}, Decisions: []string{"接入 DeepSeek"},
		ActionItems: []MeetingActionItem{{Task: "完成联调", Status: MeetingActionItemStatusPending}},
		Risks:       []string{"Provider 故障需要重试"}, Provider: "deepseek", ModelName: "deepseek-v4-flash",
		PromptVersion: "meeting-summary-v1", GeneratedAt: &now,
	}
	if err := valid.ValidateCompleted(); err != nil {
		t.Fatalf("ValidateCompleted() error = %v", err)
	}

	invalid := *valid
	invalid.ActionItems = []MeetingActionItem{{Task: "完成联调", Status: MeetingActionItemStatusUnspecified}}
	if err := invalid.ValidateCompleted(); err == nil {
		t.Fatal("expected invalid action item status to fail")
	}
}

func TestMeetingSummaryStatusTransitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		from MeetingSummaryStatus
		to   MeetingSummaryStatus
		want bool
	}{
		{MeetingSummaryStatusNotStarted, MeetingSummaryStatusPending, true},
		{MeetingSummaryStatusPending, MeetingSummaryStatusProcessing, true},
		{MeetingSummaryStatusProcessing, MeetingSummaryStatusSucceeded, true},
		{MeetingSummaryStatusProcessing, MeetingSummaryStatusFailed, true},
		{MeetingSummaryStatusSucceeded, MeetingSummaryStatusProcessing, false},
	}
	for _, test := range tests {
		if got := test.from.CanTransitionTo(test.to); got != test.want {
			t.Fatalf("%s.CanTransitionTo(%s) = %v, want %v", test.from.String(), test.to.String(), got, test.want)
		}
	}
}

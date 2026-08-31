package data

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type persistedMeetingActionItem struct {
	Assignee string `json:"assignee,omitempty"`
	Task     string `json:"task"`
	DueText  string `json:"due_text,omitempty"`
	Status   string `json:"status"`
}

// persistedMeetingSummaryContent is the current structured summary embedded in
// meetings.summary_content. Provider payloads remain in Vision's worker job.
type persistedMeetingSummaryContent struct {
	Topic          string                       `json:"topic"`
	Abstract       string                       `json:"abstract"`
	KeyDiscussions []string                     `json:"key_discussions"`
	Decisions      []string                     `json:"decisions"`
	ActionItems    []persistedMeetingActionItem `json:"action_items"`
	Risks          []string                     `json:"risks"`
}

var _ biz.MeetingSummaryRepo = (*MeetingRepo)(nil)

func (r *MeetingRepo) EnsureSummaryTask(ctx context.Context, meetingID, userID, idempotencyKey string, now time.Time) (*biz.MeetingSummaryTask, error) {
	var task *biz.MeetingSummaryTask
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		meetingRow, err := lockMeeting(ctx, tx, userID, meetingID)
		if err != nil {
			return err
		}
		transcriptionStatus, err := biz.ParseMeetingTranscriptionStatus(meetingRow.TranscriptionStatus)
		if err != nil {
			return err
		}
		meetingStatus, err := biz.ParseMeetingStatus(meetingRow.Status)
		if err != nil {
			return err
		}
		if transcriptionStatus != biz.MeetingTranscriptionStatusSucceeded || meetingRow.TranscriptRevision <= 0 ||
			meetingStatus == biz.MeetingStatusFailed || meetingStatus == biz.MeetingStatusCancelled {
			return biz.ErrMeetingStateConflict
		}

		// Core stores only the latest summary. Repeating the same key returns the
		// current task. A retried automatic completion also must not replace a
		// newer manually regenerated version.
		if meetingRow.SummaryVersion > 0 && (meetingRow.SummaryIdempotencyKey == idempotencyKey || strings.HasPrefix(idempotencyKey, "automatic:")) {
			task, err = summaryMeetingToTask(meetingRow)
			return err
		}

		version := meetingRow.SummaryVersion + 1
		updates := map[string]any{
			"summary_status":                     biz.MeetingSummaryStatusPending.String(),
			"summary_version":                    version,
			"summary_source_transcript_revision": meetingRow.TranscriptRevision,
			"summary_idempotency_key":            idempotencyKey,
			"summary_content":                    json.RawMessage("{}"),
			"summary_provider":                   "",
			"summary_model_name":                 "",
			"summary_prompt_version":             "",
			"summary_input_tokens":               int64(0),
			"summary_output_tokens":              int64(0),
			"summary_failure_reason":             "",
			"summary_generated_at":               nil,
			"updated_at":                         now,
		}
		if err := tx.WithContext(ctx).Model(meetingRow).Updates(updates).Error; err != nil {
			return err
		}
		meetingRow.SummaryStatus = biz.MeetingSummaryStatusPending.String()
		meetingRow.SummaryVersion = version
		meetingRow.SummarySourceTranscriptRevision = meetingRow.TranscriptRevision
		meetingRow.SummaryIdempotencyKey = idempotencyKey
		meetingRow.UpdatedAt = now
		task, err = summaryMeetingToTask(meetingRow)
		return err
	})
	if err != nil {
		return nil, mapMeetingDataError(err)
	}
	return task, nil
}

func (r *MeetingRepo) MarkSummaryTaskProcessing(ctx context.Context, meetingID string, version int64, now time.Time) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.Meeting
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", meetingID).Take(&row).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return biz.ErrMeetingNotFound
		}
		if err != nil {
			return err
		}
		if version < row.SummaryVersion {
			return nil
		}
		if version > row.SummaryVersion {
			return biz.ErrMeetingStateConflict
		}
		status, err := biz.ParseMeetingSummaryStatus(row.SummaryStatus)
		if err != nil {
			return err
		}
		if status == biz.MeetingSummaryStatusSucceeded || status == biz.MeetingSummaryStatusFailed || status == biz.MeetingSummaryStatusProcessing {
			return nil
		}
		if !status.CanTransitionTo(biz.MeetingSummaryStatusProcessing) {
			return biz.ErrMeetingStateConflict
		}
		return tx.WithContext(ctx).Model(&row).Updates(map[string]any{
			"summary_status": biz.MeetingSummaryStatusProcessing.String(), "updated_at": now,
		}).Error
	})
	if err != nil {
		return mapMeetingDataError(err)
	}
	return nil
}

func (r *MeetingRepo) GetSummary(ctx context.Context, userID, meetingID string) (*biz.MeetingSummaryView, error) {
	var row model.Meeting
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ? AND deleted_at IS NULL", meetingID, userID).Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrMeetingNotFound
	}
	if err != nil {
		return nil, quotaDataError(err)
	}
	status, err := biz.ParseMeetingSummaryStatus(row.SummaryStatus)
	if err != nil {
		return nil, quotaDataError(err)
	}
	view := &biz.MeetingSummaryView{Status: status}
	if status == biz.MeetingSummaryStatusSucceeded {
		view.Summary, err = meetingSummaryModelToBiz(&row)
		if err != nil {
			return nil, quotaDataError(err)
		}
	}
	if status == biz.MeetingSummaryStatusFailed {
		view.FailureReason = row.SummaryFailureReason
	}
	return view, nil
}

func (r *MeetingRepo) GetTranscriptSnapshot(ctx context.Context, meetingID string, revision int64, limit int) (*biz.MeetingTranscriptSnapshot, error) {
	if limit <= 0 || limit > 10_000 {
		return nil, fmt.Errorf("meeting transcript snapshot limit is invalid")
	}
	var meetingRow model.Meeting
	err := r.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", meetingID).Take(&meetingRow).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrMeetingNotFound
	}
	if err != nil {
		return nil, quotaDataError(err)
	}
	if meetingRow.TranscriptRevision != revision {
		return nil, biz.ErrMeetingStateConflict
	}
	language, err := biz.ParseMeetingLanguage(meetingRow.Language)
	if err != nil {
		return nil, quotaDataError(err)
	}
	var rows []model.MeetingTranscriptSegment
	if err := r.db.WithContext(ctx).Where("meeting_id = ?", meetingID).Order("sequence_no ASC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, quotaDataError(err)
	}
	if len(rows) > limit {
		return nil, biz.ErrMeetingStateConflict
	}
	segments := make([]*biz.MeetingTranscriptSegment, 0, len(rows))
	for index := range rows {
		segment, err := toBizMeetingTranscriptSegment(&rows[index])
		if err != nil {
			return nil, quotaDataError(err)
		}
		segments = append(segments, segment)
	}
	return &biz.MeetingTranscriptSnapshot{
		MeetingID: meetingID, Language: language, TranscriptRevision: revision, Segments: segments,
	}, nil
}

func (r *MeetingRepo) CompleteSummary(ctx context.Context, command biz.CompleteMeetingSummaryCommand) (*biz.MeetingSummary, error) {
	var output *biz.MeetingSummary
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.Meeting
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", command.MeetingID).Take(&row).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return biz.ErrMeetingNotFound
		}
		if err != nil {
			return err
		}
		if command.Version < row.SummaryVersion {
			// An older Vision job completed after a newer regeneration request.
			// Ignore it so it cannot overwrite the current summary.
			output = command.Summary
			return nil
		}
		if command.Version > row.SummaryVersion || row.SummarySourceTranscriptRevision != command.SourceTranscriptRevision ||
			row.TranscriptRevision != command.SourceTranscriptRevision {
			return biz.ErrMeetingStateConflict
		}
		status, err := biz.ParseMeetingSummaryStatus(row.SummaryStatus)
		if err != nil {
			return err
		}
		if status == biz.MeetingSummaryStatusSucceeded {
			output, err = meetingSummaryModelToBiz(&row)
			return err
		}
		if !status.CanTransitionTo(biz.MeetingSummaryStatusSucceeded) {
			return biz.ErrMeetingStateConflict
		}
		updates, err := meetingSummaryBizToUpdates(command.Summary)
		if err != nil {
			return err
		}
		updates["summary_status"] = biz.MeetingSummaryStatusSucceeded.String()
		updates["status"] = biz.MeetingStatusCompleted.String()
		updates["updated_at"] = command.Summary.UpdatedAt
		if err := tx.WithContext(ctx).Model(&row).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Where("id = ?", row.ID).Take(&row).Error; err != nil {
			return err
		}
		output, err = meetingSummaryModelToBiz(&row)
		return err
	})
	if err != nil {
		return nil, mapMeetingDataError(err)
	}
	return output, nil
}

func (r *MeetingRepo) FailSummary(ctx context.Context, command biz.FailMeetingSummaryCommand) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.Meeting
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", command.MeetingID).Take(&row).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return biz.ErrMeetingNotFound
		}
		if err != nil {
			return err
		}
		if command.Version < row.SummaryVersion {
			return nil
		}
		if command.Version > row.SummaryVersion || row.SummarySourceTranscriptRevision != command.SourceTranscriptRevision {
			return biz.ErrMeetingStateConflict
		}
		status, err := biz.ParseMeetingSummaryStatus(row.SummaryStatus)
		if err != nil {
			return err
		}
		if status == biz.MeetingSummaryStatusFailed || status == biz.MeetingSummaryStatusSucceeded {
			return nil
		}
		if !status.CanTransitionTo(biz.MeetingSummaryStatusFailed) {
			return biz.ErrMeetingStateConflict
		}
		return tx.WithContext(ctx).Model(&row).Updates(map[string]any{
			"summary_status":         biz.MeetingSummaryStatusFailed.String(),
			"summary_failure_reason": command.Reason.String(),
			"summary_content":        json.RawMessage("{}"),
			"summary_generated_at":   nil,
			"status":                 biz.MeetingStatusPartiallyCompleted.String(),
			"updated_at":             command.FailedAt,
		}).Error
	})
	if err != nil {
		return mapMeetingDataError(err)
	}
	return nil
}

func summaryMeetingToTask(row *model.Meeting) (*biz.MeetingSummaryTask, error) {
	if row == nil {
		return nil, fmt.Errorf("meeting summary task row is required")
	}
	language, err := biz.ParseMeetingLanguage(row.Language)
	if err != nil {
		return nil, err
	}
	return &biz.MeetingSummaryTask{
		MeetingID: row.ID, UserID: row.UserID, Version: row.SummaryVersion,
		SourceTranscriptRevision: row.SummarySourceTranscriptRevision, Language: language,
		IdempotencyKey: row.SummaryIdempotencyKey,
	}, nil
}

func meetingSummaryBizToUpdates(summary *biz.MeetingSummary) (map[string]any, error) {
	content := persistedMeetingSummaryContent{
		Topic: summary.Topic, Abstract: summary.Abstract,
		KeyDiscussions: summary.KeyDiscussions, Decisions: summary.Decisions, Risks: summary.Risks,
		ActionItems: make([]persistedMeetingActionItem, 0, len(summary.ActionItems)),
	}
	for _, item := range summary.ActionItems {
		content.ActionItems = append(content.ActionItems, persistedMeetingActionItem{
			Assignee: item.Assignee, Task: item.Task, DueText: item.DueText, Status: item.Status.String(),
		})
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"summary_content": json.RawMessage(raw), "summary_provider": summary.Provider,
		"summary_model_name": summary.ModelName, "summary_prompt_version": summary.PromptVersion,
		"summary_input_tokens": summary.InputTokens, "summary_output_tokens": summary.OutputTokens,
		"summary_failure_reason": "", "summary_generated_at": summary.GeneratedAt,
	}, nil
}

func meetingSummaryModelToBiz(row *model.Meeting) (*biz.MeetingSummary, error) {
	if row == nil {
		return nil, fmt.Errorf("meeting row is nil")
	}
	status, err := biz.ParseMeetingSummaryStatus(row.SummaryStatus)
	if err != nil {
		return nil, err
	}
	if status != biz.MeetingSummaryStatusSucceeded || len(row.SummaryContent) == 0 {
		return nil, fmt.Errorf("stored meeting summary is not complete")
	}
	var content persistedMeetingSummaryContent
	if err := json.Unmarshal(row.SummaryContent, &content); err != nil {
		return nil, fmt.Errorf("decode stored meeting summary JSON: %w", err)
	}
	actionItems := make([]biz.MeetingActionItem, 0, len(content.ActionItems))
	for _, item := range content.ActionItems {
		itemStatus, err := biz.ParseMeetingActionItemStatus(item.Status)
		if err != nil {
			return nil, err
		}
		actionItems = append(actionItems, biz.MeetingActionItem{
			Assignee: item.Assignee, Task: item.Task, DueText: item.DueText, Status: itemStatus,
		})
	}
	return &biz.MeetingSummary{
		ID: row.ID, MeetingID: row.ID, Version: row.SummaryVersion,
		SourceTranscriptRevision: row.SummarySourceTranscriptRevision, Status: status,
		Topic: content.Topic, Abstract: content.Abstract, KeyDiscussions: content.KeyDiscussions,
		Decisions: content.Decisions, ActionItems: actionItems, Risks: content.Risks,
		Provider: row.SummaryProvider, ModelName: row.SummaryModelName, PromptVersion: row.SummaryPromptVersion,
		InputTokens: row.SummaryInputTokens, OutputTokens: row.SummaryOutputTokens,
		FailureReason: row.SummaryFailureReason, GeneratedAt: row.SummaryGeneratedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

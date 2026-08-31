package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxStoredLLMPayloadBytes = 2 << 20
	maxStoredLLMFailureRunes = 2_000
)

type MeetingSummaryRepo struct {
	db *gorm.DB
}

var _ biz.MeetingSummaryRepo = (*MeetingSummaryRepo)(nil)

func NewMeetingSummaryRepo(db *gorm.DB) (*MeetingSummaryRepo, error) {
	if db == nil {
		return nil, fmt.Errorf("meeting summary database is required")
	}
	return &MeetingSummaryRepo{db: db}, nil
}

func (r *MeetingSummaryRepo) RecordLLMRequest(ctx context.Context, jobID, payload string, startedAt time.Time) error {
	if _, err := uuid.Parse(jobID); err != nil || startedAt.IsZero() || len(payload) == 0 || len(payload) > maxStoredLLMPayloadBytes {
		return fmt.Errorf("meeting summary LLM request record is invalid")
	}
	result := r.db.WithContext(ctx).Model(&model.MeetingSummaryJob{}).
		Where("id = ? AND status = ?", jobID, string(biz.MeetingSummaryJobStatusProcessing)).
		Updates(map[string]any{
			"llm_request": payload, "llm_response": "", "llm_http_status": int32(0),
			"llm_duration_milliseconds": int64(0), "llm_failure": "", "updated_at": startedAt,
		})
	if result.Error != nil {
		return fmt.Errorf("record meeting summary LLM request: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("meeting summary job is not processing")
	}
	return nil
}

func (r *MeetingSummaryRepo) RecordLLMResponse(
	ctx context.Context,
	jobID, payload string,
	httpStatus int32,
	duration time.Duration,
	inputTokens, outputTokens int64,
	failure string,
	completedAt time.Time,
) error {
	if _, err := uuid.Parse(jobID); err != nil || completedAt.IsZero() || len(payload) > maxStoredLLMPayloadBytes ||
		httpStatus < 0 || httpStatus > 599 || duration < 0 || inputTokens < 0 || outputTokens < 0 ||
		utf8.RuneCountInString(failure) > maxStoredLLMFailureRunes {
		return fmt.Errorf("meeting summary LLM response record is invalid")
	}
	durationMS := duration.Milliseconds()
	if duration > 0 && durationMS == 0 {
		durationMS = 1
	}
	result := r.db.WithContext(ctx).Model(&model.MeetingSummaryJob{}).
		Where("id = ? AND status = ?", jobID, string(biz.MeetingSummaryJobStatusProcessing)).
		Updates(map[string]any{
			"llm_response": payload, "llm_http_status": httpStatus,
			"llm_duration_milliseconds": durationMS, "llm_failure": failure,
			"input_tokens": inputTokens, "output_tokens": outputTokens, "updated_at": completedAt,
		})
	if result.Error != nil {
		return fmt.Errorf("record meeting summary LLM response: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("meeting summary job is not processing")
	}
	return nil
}

func (r *MeetingSummaryRepo) EnsureJob(ctx context.Context, input biz.PrepareMeetingSummaryInput, provider biz.MeetingSummaryProviderSnapshot) (*biz.MeetingSummaryJob, error) {
	if err := provider.Validate(); err != nil {
		return nil, err
	}
	row := model.MeetingSummaryJob{
		ID: uuid.NewString(), ProviderConfigID: provider.ConfigID,
		MeetingID: input.MeetingID, UserID: input.UserID,
		Version: input.Version, SourceTranscriptRevision: input.SourceTranscriptRevision,
		Language: string(input.Language), IdempotencyKey: input.IdempotencyKey,
		Status: string(biz.MeetingSummaryJobStatusPending), Provider: string(provider.Provider),
		ModelName: provider.ModelName, PromptVersion: provider.PromptVersion, AvailableAt: input.Now,
		CreatedAt: input.Now, UpdatedAt: input.Now,
	}
	insert := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "meeting_id"}, {Name: "idempotency_key"}}, DoNothing: true,
	}).Create(&row)
	if insert.Error != nil {
		return nil, fmt.Errorf("create meeting summary job: %w", insert.Error)
	}
	if insert.RowsAffected == 0 {
		if err := r.db.WithContext(ctx).Where("meeting_id = ? AND idempotency_key = ?", input.MeetingID, input.IdempotencyKey).Take(&row).Error; err != nil {
			return nil, fmt.Errorf("load existing meeting summary job: %w", err)
		}
		if row.UserID != input.UserID || row.Version != input.Version || row.SourceTranscriptRevision != input.SourceTranscriptRevision ||
			row.Language != string(input.Language) {
			return nil, fmt.Errorf("meeting summary idempotency key conflicts with existing job")
		}
	}
	return meetingSummaryJobModelToBiz(&row)
}

func (r *MeetingSummaryRepo) ClaimJobs(ctx context.Context, now time.Time, leaseTimeout time.Duration, limit int) ([]*biz.MeetingSummaryJob, error) {
	if leaseTimeout <= 0 || limit <= 0 || limit > 20 {
		return nil, fmt.Errorf("meeting summary claim parameters are invalid")
	}
	jobs := make([]*biz.MeetingSummaryJob, 0, limit)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []model.MeetingSummaryJob
		leaseExpiredAt := now.Add(-leaseTimeout)
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("available_at <= ?", now).
			Where("status IN ? OR (status = ? AND updated_at <= ?)", []string{
				string(biz.MeetingSummaryJobStatusPending),
				string(biz.MeetingSummaryJobStatusDeliveryPending),
				string(biz.MeetingSummaryJobStatusFailureDeliveryPending),
			}, string(biz.MeetingSummaryJobStatusProcessing), leaseExpiredAt).
			Order("created_at ASC").Order("id ASC").Limit(limit).Find(&rows).Error
		if err != nil {
			return fmt.Errorf("claim meeting summary jobs: %w", err)
		}
		for index := range rows {
			row := &rows[index]
			status, err := biz.ParseMeetingSummaryJobStatus(row.Status)
			if err != nil {
				return err
			}
			if status == biz.MeetingSummaryJobStatusPending || status == biz.MeetingSummaryJobStatusProcessing {
				updates := map[string]any{"status": string(biz.MeetingSummaryJobStatusProcessing), "updated_at": now}
				if row.StartedAt == nil {
					updates["started_at"] = now
					row.StartedAt = &now
				}
				result := tx.WithContext(ctx).Model(row).Updates(updates)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf("claimed meeting summary job disappeared")
				}
				row.Status = string(biz.MeetingSummaryJobStatusProcessing)
				row.UpdatedAt = now
			}
			job, err := meetingSummaryJobModelToBiz(row)
			if err != nil {
				return err
			}
			jobs = append(jobs, job)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *MeetingSummaryRepo) SaveGenerated(ctx context.Context, jobID string, summary *biz.MeetingSummary, now time.Time) error {
	raw, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encode generated meeting summary: %w", err)
	}
	result := r.db.WithContext(ctx).Model(&model.MeetingSummaryJob{}).
		Where("id = ? AND status = ?", jobID, string(biz.MeetingSummaryJobStatusProcessing)).
		Updates(map[string]any{
			"status": string(biz.MeetingSummaryJobStatusDeliveryPending), "result_json": json.RawMessage(raw),
			"input_tokens": summary.InputTokens, "output_tokens": summary.OutputTokens,
			"failure_reason": "", "updated_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("save generated meeting summary: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("meeting summary job is not processing")
	}
	return nil
}

func (r *MeetingSummaryRepo) SaveFailureForDelivery(ctx context.Context, jobID string, reason biz.MeetingSummaryFailureReason, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.MeetingSummaryJob{}).
		Where("id = ? AND status IN ?", jobID, []string{string(biz.MeetingSummaryJobStatusPending), string(biz.MeetingSummaryJobStatusProcessing)}).
		Updates(map[string]any{
			"status":         string(biz.MeetingSummaryJobStatusFailureDeliveryPending),
			"failure_reason": reason.String(), "attempt_count": gorm.Expr("attempt_count + 1"), "updated_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("save meeting summary failure: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("meeting summary job cannot record failure")
	}
	return nil
}

func (r *MeetingSummaryRepo) RetryJob(ctx context.Context, jobID string, availableAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.MeetingSummaryJob
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", jobID).Take(&row).Error
		if err != nil {
			return err
		}
		status, err := biz.ParseMeetingSummaryJobStatus(row.Status)
		if err != nil {
			return err
		}
		nextStatus := status
		if status == biz.MeetingSummaryJobStatusProcessing {
			nextStatus = biz.MeetingSummaryJobStatusPending
		}
		if nextStatus != biz.MeetingSummaryJobStatusPending && nextStatus != biz.MeetingSummaryJobStatusDeliveryPending &&
			nextStatus != biz.MeetingSummaryJobStatusFailureDeliveryPending {
			return fmt.Errorf("meeting summary job is not retryable")
		}
		return tx.WithContext(ctx).Model(&row).Updates(map[string]any{
			"status": string(nextStatus), "attempt_count": gorm.Expr("attempt_count + 1"),
			"available_at": availableAt, "updated_at": time.Now().UTC(),
		}).Error
	})
}

func (r *MeetingSummaryRepo) MarkSucceeded(ctx context.Context, jobID string, now time.Time) error {
	return r.markTerminal(ctx, jobID, biz.MeetingSummaryJobStatusDeliveryPending, biz.MeetingSummaryJobStatusSucceeded, now)
}

func (r *MeetingSummaryRepo) MarkFailed(ctx context.Context, jobID string, now time.Time) error {
	return r.markTerminal(ctx, jobID, biz.MeetingSummaryJobStatusFailureDeliveryPending, biz.MeetingSummaryJobStatusFailed, now)
}

func (r *MeetingSummaryRepo) markTerminal(ctx context.Context, jobID string, current, next biz.MeetingSummaryJobStatus, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.MeetingSummaryJob{}).
		Where("id = ? AND status = ?", jobID, string(current)).
		Updates(map[string]any{"status": string(next), "finished_at": now, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("mark meeting summary job terminal: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("meeting summary job is not in delivery state")
	}
	return nil
}

func meetingSummaryJobModelToBiz(row *model.MeetingSummaryJob) (*biz.MeetingSummaryJob, error) {
	if row == nil {
		return nil, fmt.Errorf("meeting summary job row is nil")
	}
	status, err := biz.ParseMeetingSummaryJobStatus(row.Status)
	if err != nil {
		return nil, err
	}
	language, err := biz.ParseMeetingLanguage(row.Language)
	if err != nil {
		return nil, err
	}
	provider, err := biz.ParseMeetingSummaryProviderName(row.Provider)
	if err != nil {
		return nil, err
	}
	job := &biz.MeetingSummaryJob{
		ID: row.ID, ProviderConfigID: row.ProviderConfigID,
		MeetingID: row.MeetingID, UserID: row.UserID, Version: row.Version,
		SourceTranscriptRevision: row.SourceTranscriptRevision, Language: language,
		IdempotencyKey: row.IdempotencyKey, Status: status, Provider: provider,
		ModelName: row.ModelName, PromptVersion: row.PromptVersion,
		AttemptCount: row.AttemptCount, AvailableAt: row.AvailableAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if len(row.ResultJSON) > 0 {
		var summary biz.MeetingSummary
		if err := json.Unmarshal(row.ResultJSON, &summary); err != nil {
			return nil, fmt.Errorf("decode stored meeting summary result: %w", err)
		}
		summary.Provider = provider
		summary.ModelName = row.ModelName
		summary.PromptVersion = row.PromptVersion
		summary.InputTokens = row.InputTokens
		summary.OutputTokens = row.OutputTokens
		job.Result = &summary
	}
	if row.FailureReason != "" {
		job.FailureReason, err = biz.ParseMeetingSummaryFailureReason(row.FailureReason)
		if err != nil {
			return nil, err
		}
	}
	return job, nil
}

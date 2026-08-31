package data

import (
	"context"
	"fmt"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ biz.TranscriptionOutboxRepo = (*TranscriptionRepo)(nil)

func (r *TranscriptionRepo) ClaimTranscriptionDeliveries(ctx context.Context, now time.Time, leaseTimeout time.Duration, limit int, maxAttempts int32) ([]*biz.TranscriptionOutboxDelivery, error) {
	if ctx == nil || leaseTimeout <= 0 || limit <= 0 || limit > 100 || maxAttempts <= 0 {
		return nil, fmt.Errorf("transcription outbox claim parameters are invalid")
	}
	deliveries := make([]*biz.TranscriptionOutboxDelivery, 0, limit)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []model.TranscriptionOutbox
		leaseExpiredAt := now.Add(-leaseTimeout)
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("attempt_count < ?", maxAttempts).
			Where("((status = ? AND available_at <= ?) OR (status = ? AND updated_at <= ?))",
				string(biz.TranscriptionOutboxStatusPending), now, string(biz.TranscriptionOutboxStatusProcessing), leaseExpiredAt).
			Where(`event_type <> ? OR NOT EXISTS (
				SELECT 1 FROM transcription_outbox dependency
				WHERE dependency.session_id = transcription_outbox.session_id
				  AND dependency.event_type = ? AND dependency.status <> ?
			)`, string(biz.TranscriptionOutboxEventTypeUsageReady), string(biz.TranscriptionOutboxEventTypeFinalTranscriptReady), string(biz.TranscriptionOutboxStatusDelivered)).
			Order("created_at ASC").Order("CASE WHEN event_type = 'final_transcript_ready' THEN 0 ELSE 1 END ASC").Order("id ASC").
			Limit(limit).Find(&rows).Error
		if err != nil {
			return fmt.Errorf("claim transcription outbox rows: %w", err)
		}
		for index := range rows {
			row := &rows[index]
			eventType, err := biz.ParseTranscriptionOutboxEventType(row.EventType)
			if err != nil {
				return err
			}
			if _, err := biz.ParseTranscriptionOutboxStatus(row.Status); err != nil {
				return err
			}
			update := tx.WithContext(ctx).Model(row).Updates(map[string]any{
				"status": string(biz.TranscriptionOutboxStatusProcessing), "updated_at": now,
			})
			if update.Error != nil {
				return fmt.Errorf("mark transcription outbox processing: %w", update.Error)
			}
			if update.RowsAffected != 1 {
				return fmt.Errorf("claimed transcription outbox row disappeared")
			}
			var sessionRow model.TranscriptionSession
			if err := tx.WithContext(ctx).Where("id = ?", row.SessionID).Take(&sessionRow).Error; err != nil {
				return fmt.Errorf("load transcription outbox session: %w", err)
			}
			session, err := transcriptionModelToBiz(&sessionRow)
			if err != nil {
				return err
			}
			delivery := &biz.TranscriptionOutboxDelivery{ID: row.ID, Type: eventType, AttemptCount: row.AttemptCount, Session: session}
			if eventType == biz.TranscriptionOutboxEventTypeFinalTranscriptReady {
				var segmentRows []model.TranscriptionFinalSegment
				if err := tx.WithContext(ctx).Where("session_id = ?", row.SessionID).Order("sequence_no ASC").Find(&segmentRows).Error; err != nil {
					return fmt.Errorf("load final transcription segments: %w", err)
				}
				delivery.Segments = make([]biz.TranscriptSegment, 0, len(segmentRows))
				for segmentIndex := range segmentRows {
					segment, err := transcriptionFinalSegmentToBiz(&segmentRows[segmentIndex])
					if err != nil {
						return err
					}
					delivery.Segments = append(delivery.Segments, segment)
				}
			}
			deliveries = append(deliveries, delivery)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return deliveries, nil
}

func (r *TranscriptionRepo) MarkTranscriptionDeliveryDelivered(ctx context.Context, eventID string, deliveredAt time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.TranscriptionOutbox{}).
		Where("id = ? AND status = ?", eventID, string(biz.TranscriptionOutboxStatusProcessing)).
		Updates(map[string]any{"status": string(biz.TranscriptionOutboxStatusDelivered), "delivered_at": deliveredAt, "updated_at": deliveredAt})
	if result.Error != nil {
		return fmt.Errorf("mark transcription delivery delivered: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("transcription delivery is not processing")
	}
	return nil
}

func (r *TranscriptionRepo) RetryTranscriptionDelivery(ctx context.Context, eventID string, availableAt time.Time, terminal bool) error {
	status := biz.TranscriptionOutboxStatusPending
	if terminal {
		status = biz.TranscriptionOutboxStatusFailed
	}
	result := r.db.WithContext(ctx).Model(&model.TranscriptionOutbox{}).
		Where("id = ? AND status = ?", eventID, string(biz.TranscriptionOutboxStatusProcessing)).
		Updates(map[string]any{
			"status": status, "attempt_count": gorm.Expr("attempt_count + 1"),
			"available_at": availableAt, "updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return fmt.Errorf("reschedule transcription delivery: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("transcription delivery is not processing")
	}
	return nil
}

func transcriptionFinalSegmentToBiz(row *model.TranscriptionFinalSegment) (biz.TranscriptSegment, error) {
	if row == nil || row.StartMilliseconds < 0 || row.EndMilliseconds < row.StartMilliseconds {
		return biz.TranscriptSegment{}, fmt.Errorf("stored final transcription segment is invalid")
	}
	language, err := biz.ParseMeetingLanguage(row.Language)
	if err != nil {
		return biz.TranscriptSegment{}, err
	}
	segment := biz.TranscriptSegment{
		ID: row.ID, SessionID: row.SessionID, Sequence: row.SequenceNo,
		StartOffset:  time.Duration(row.StartMilliseconds) * time.Millisecond,
		EndOffset:    time.Duration(row.EndMilliseconds) * time.Millisecond,
		SpeakerLabel: row.SpeakerLabel, Content: row.Content, Language: language,
		Confidence: row.Confidence, CreatedAt: row.CreatedAt,
	}
	if err := segment.Validate(); err != nil {
		return biz.TranscriptSegment{}, err
	}
	return segment, nil
}

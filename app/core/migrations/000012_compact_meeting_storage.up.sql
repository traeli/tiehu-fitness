BEGIN;

ALTER TABLE meetings
    ADD COLUMN quota_period_start TIMESTAMPTZ,
    ADD COLUMN quota_period_end TIMESTAMPTZ,
    ADD COLUMN reported_audio_seconds BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN actual_audio_seconds BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN provider_usage_seconds BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN quota_status VARCHAR(16),
    ADD COLUMN quota_expires_at TIMESTAMPTZ,
    ADD COLUMN quota_finalized_at TIMESTAMPTZ,
    ADD COLUMN quota_settlement_reason VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN transcript_segments JSONB NOT NULL DEFAULT '[]'::jsonb;

UPDATE meetings AS meeting
SET quota_period_start = reservation.period_start,
    quota_period_end = reservation.period_end,
    reported_audio_seconds = reservation.reported_seconds,
    actual_audio_seconds = COALESCE(usage.actual_seconds, 0),
    provider_usage_seconds = COALESCE(usage.provider_usage_seconds, 0),
    quota_status = reservation.status,
    quota_expires_at = reservation.expires_at,
    quota_finalized_at = reservation.finalized_at,
    quota_settlement_reason = COALESCE(usage.settlement_reason, '')
FROM meeting_usage_reservations AS reservation
LEFT JOIN meeting_usage_records AS usage ON usage.reservation_id = reservation.reservation_id
WHERE meeting.reservation_id = reservation.reservation_id;

UPDATE meetings AS meeting
SET transcript_segments = transcript.payload
FROM (
    SELECT meeting_id, jsonb_agg(jsonb_build_object(
        'id', segment_id,
        'sequence_no', sequence_no,
        'start_offset_ms', start_offset_ms,
        'end_offset_ms', end_offset_ms,
        'speaker_label', speaker_label,
        'content', content,
        'language', language,
        'confidence', confidence,
        'created_at', created_at
    ) ORDER BY sequence_no) AS payload
    FROM meeting_transcript_segments
    GROUP BY meeting_id
) AS transcript
WHERE meeting.id = transcript.meeting_id;

-- The legacy meeting-to-reservation foreign key is initially deferred. Flush
-- its pending trigger events before altering meetings in the same transaction.
SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE meetings
    ALTER COLUMN quota_period_start SET NOT NULL,
    ALTER COLUMN quota_period_end SET NOT NULL,
    ALTER COLUMN quota_status SET NOT NULL,
    ALTER COLUMN quota_status SET DEFAULT 'active',
    ALTER COLUMN quota_expires_at SET NOT NULL,
    DROP CONSTRAINT fk_meeting_usage_reservation,
    ADD CONSTRAINT chk_meeting_quota_period CHECK (quota_period_end > quota_period_start),
    ADD CONSTRAINT chk_meeting_quota_reported CHECK (reported_audio_seconds BETWEEN 0 AND granted_audio_seconds),
    ADD CONSTRAINT chk_meeting_quota_actual CHECK (actual_audio_seconds BETWEEN 0 AND granted_audio_seconds),
    ADD CONSTRAINT chk_meeting_quota_provider_usage CHECK (provider_usage_seconds >= 0),
    ADD CONSTRAINT chk_meeting_quota_status CHECK (quota_status IN ('active', 'settled', 'released', 'expired')),
    ADD CONSTRAINT chk_meeting_quota_finalized CHECK (
        (quota_status = 'active' AND quota_finalized_at IS NULL AND quota_settlement_reason = '') OR
        (quota_status <> 'active' AND quota_finalized_at IS NOT NULL AND quota_settlement_reason IN (
            'completed', 'quota_exhausted', 'cancelled', 'failed', 'expired', 'preparation_failed'
        ))
    ),
    ADD CONSTRAINT chk_meeting_transcript_segments_array CHECK (jsonb_typeof(transcript_segments) = 'array');

CREATE INDEX idx_meeting_active_quota
    ON meetings(user_id, quota_period_start, quota_expires_at)
    WHERE quota_status = 'active' AND deleted_at IS NULL;

ALTER TABLE meeting_usage_periods RENAME TO user_meeting_monthly_quotas;
ALTER TABLE orders RENAME COLUMN usage_period_id TO monthly_quota_id;
ALTER INDEX idx_orders_usage_period RENAME TO idx_orders_monthly_quota;

DROP TABLE meeting_transcript_batches;
DROP TABLE meeting_transcript_segments;
DROP TABLE meeting_usage_records;
DROP TABLE meeting_usage_reservations;
DROP TABLE user_meeting_quota_overrides;

COMMIT;

BEGIN;

CREATE TABLE user_meeting_quota_overrides (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    monthly_audio_seconds BIGINT,
    max_meeting_audio_seconds BIGINT,
    max_concurrent_meetings INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_meeting_quota_override_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT chk_meeting_quota_override_monthly CHECK (monthly_audio_seconds IS NULL OR monthly_audio_seconds BETWEEN 1 AND 31622400),
    CONSTRAINT chk_meeting_quota_override_single CHECK (max_meeting_audio_seconds IS NULL OR max_meeting_audio_seconds BETWEEN 1 AND 86400),
    CONSTRAINT chk_meeting_quota_override_concurrency CHECK (max_concurrent_meetings IS NULL OR max_concurrent_meetings BETWEEN 1 AND 1000)
);

ALTER TABLE user_meeting_monthly_quotas RENAME TO meeting_usage_periods;
ALTER TABLE orders RENAME COLUMN monthly_quota_id TO usage_period_id;
ALTER INDEX idx_orders_monthly_quota RENAME TO idx_orders_usage_period;

CREATE TABLE meeting_usage_reservations (
    reservation_id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    meeting_id UUID NOT NULL UNIQUE,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    granted_seconds BIGINT NOT NULL,
    reported_seconds BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    finalized_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_meeting_usage_reservation_period FOREIGN KEY (user_id, period_start)
        REFERENCES meeting_usage_periods(user_id, period_start) ON DELETE CASCADE,
    CONSTRAINT chk_meeting_usage_reservation_period CHECK (period_end > period_start),
    CONSTRAINT chk_meeting_usage_reservation_granted CHECK (granted_seconds > 0),
    CONSTRAINT chk_meeting_usage_reservation_reported CHECK (reported_seconds BETWEEN 0 AND granted_seconds),
    CONSTRAINT chk_meeting_usage_reservation_status CHECK (status IN ('active', 'settled', 'released', 'expired')),
    CONSTRAINT chk_meeting_usage_reservation_finalized CHECK (
        (status = 'active' AND finalized_at IS NULL) OR (status <> 'active' AND finalized_at IS NOT NULL)
    )
);

CREATE INDEX idx_meeting_usage_reservation_active
    ON meeting_usage_reservations(user_id, period_start, expires_at) WHERE status = 'active';

INSERT INTO meeting_usage_reservations (
    reservation_id, user_id, meeting_id, period_start, period_end,
    granted_seconds, reported_seconds, status, expires_at, finalized_at, created_at, updated_at
)
SELECT reservation_id, user_id, id, quota_period_start, quota_period_end,
       granted_audio_seconds, reported_audio_seconds, quota_status, quota_expires_at,
       quota_finalized_at, created_at, updated_at
FROM meetings;

CREATE TABLE meeting_usage_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reservation_id UUID NOT NULL UNIQUE REFERENCES meeting_usage_reservations(reservation_id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    meeting_id UUID NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    usage_kind VARCHAR(24) NOT NULL,
    actual_seconds BIGINT NOT NULL,
    provider_usage_seconds BIGINT NOT NULL DEFAULT 0,
    settlement_reason VARCHAR(32) NOT NULL,
    settled_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_meeting_usage_record_meeting_kind UNIQUE (meeting_id, usage_kind),
    CONSTRAINT chk_meeting_usage_record_period CHECK (period_end > period_start),
    CONSTRAINT chk_meeting_usage_record_kind CHECK (usage_kind = 'asr_audio'),
    CONSTRAINT chk_meeting_usage_record_actual CHECK (actual_seconds >= 0),
    CONSTRAINT chk_meeting_usage_record_provider CHECK (provider_usage_seconds >= 0),
    CONSTRAINT chk_meeting_usage_record_reason CHECK (
        settlement_reason IN ('completed', 'quota_exhausted', 'cancelled', 'failed', 'expired', 'preparation_failed')
    )
);

CREATE INDEX idx_meeting_usage_record_user_period
    ON meeting_usage_records(user_id, period_start, settled_at);

INSERT INTO meeting_usage_records (
    reservation_id, user_id, meeting_id, period_start, period_end, usage_kind,
    actual_seconds, provider_usage_seconds, settlement_reason, settled_at, created_at, updated_at
)
SELECT reservation_id, user_id, id, quota_period_start, quota_period_end, 'asr_audio',
       actual_audio_seconds, provider_usage_seconds, quota_settlement_reason,
       quota_finalized_at, quota_finalized_at, updated_at
FROM meetings
WHERE quota_status <> 'active';

CREATE TABLE meeting_transcript_segments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    meeting_id UUID NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
    segment_id UUID NOT NULL,
    sequence_no BIGINT NOT NULL,
    start_offset_ms BIGINT NOT NULL,
    end_offset_ms BIGINT NOT NULL,
    speaker_label VARCHAR(80) NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    language VARCHAR(16) NOT NULL,
    confidence REAL,
    created_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_meeting_segment_sequence UNIQUE (meeting_id, sequence_no),
    CONSTRAINT uk_meeting_segment_id UNIQUE (meeting_id, segment_id),
    CONSTRAINT chk_meeting_segment_sequence CHECK (sequence_no > 0),
    CONSTRAINT chk_meeting_segment_offsets CHECK (start_offset_ms >= 0 AND end_offset_ms >= start_offset_ms),
    CONSTRAINT chk_meeting_segment_content CHECK (char_length(content) BETWEEN 1 AND 20000),
    CONSTRAINT chk_meeting_segment_language CHECK (language IN ('auto', 'zh_cn', 'en_us')),
    CONSTRAINT chk_meeting_segment_confidence CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1)
);

CREATE INDEX idx_meeting_segment_page ON meeting_transcript_segments(meeting_id, sequence_no ASC);

INSERT INTO meeting_transcript_segments (
    meeting_id, segment_id, sequence_no, start_offset_ms, end_offset_ms,
    speaker_label, content, language, confidence, created_at
)
SELECT meeting.id,
       (segment->>'id')::uuid,
       (segment->>'sequence_no')::bigint,
       (segment->>'start_offset_ms')::bigint,
       (segment->>'end_offset_ms')::bigint,
       COALESCE(segment->>'speaker_label', ''),
       segment->>'content',
       segment->>'language',
       NULLIF(segment->>'confidence', '')::real,
       (segment->>'created_at')::timestamptz
FROM meetings AS meeting
CROSS JOIN LATERAL jsonb_array_elements(meeting.transcript_segments) AS segment;

CREATE TABLE meeting_transcript_batches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    meeting_id UUID NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
    batch_id UUID NOT NULL,
    last_sequence_no BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_meeting_transcript_batch UNIQUE (meeting_id, batch_id),
    CONSTRAINT chk_meeting_transcript_batch_sequence CHECK (last_sequence_no > 0)
);

ALTER TABLE meetings
    ADD CONSTRAINT fk_meeting_usage_reservation FOREIGN KEY (reservation_id)
        REFERENCES meeting_usage_reservations(reservation_id) DEFERRABLE INITIALLY DEFERRED,
    DROP CONSTRAINT chk_meeting_quota_period,
    DROP CONSTRAINT chk_meeting_quota_reported,
    DROP CONSTRAINT chk_meeting_quota_actual,
    DROP CONSTRAINT chk_meeting_quota_provider_usage,
    DROP CONSTRAINT chk_meeting_quota_status,
    DROP CONSTRAINT chk_meeting_quota_finalized,
    DROP CONSTRAINT chk_meeting_transcript_segments_array,
    DROP COLUMN quota_period_start,
    DROP COLUMN quota_period_end,
    DROP COLUMN reported_audio_seconds,
    DROP COLUMN actual_audio_seconds,
    DROP COLUMN provider_usage_seconds,
    DROP COLUMN quota_status,
    DROP COLUMN quota_expires_at,
    DROP COLUMN quota_finalized_at,
    DROP COLUMN quota_settlement_reason,
    DROP COLUMN transcript_segments;

COMMIT;

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

CREATE TABLE meeting_usage_periods (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    consumed_seconds BIGINT NOT NULL DEFAULT 0,
    reserved_seconds BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_meeting_usage_period_user_start UNIQUE (user_id, period_start),
    CONSTRAINT chk_meeting_usage_period_range CHECK (period_end > period_start),
    CONSTRAINT chk_meeting_usage_period_consumed CHECK (consumed_seconds >= 0),
    CONSTRAINT chk_meeting_usage_period_reserved CHECK (reserved_seconds >= 0)
);

CREATE TABLE meeting_usage_reservations (
    reservation_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    meeting_id UUID NOT NULL UNIQUE,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    granted_seconds BIGINT NOT NULL,
    reported_seconds BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    expires_at TIMESTAMPTZ NOT NULL,
    finalized_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_meeting_usage_reservation_period FOREIGN KEY (user_id, period_start)
        REFERENCES meeting_usage_periods(user_id, period_start) ON DELETE CASCADE,
    CONSTRAINT chk_meeting_usage_reservation_period CHECK (period_end > period_start),
    CONSTRAINT chk_meeting_usage_reservation_granted CHECK (granted_seconds > 0),
    CONSTRAINT chk_meeting_usage_reservation_reported CHECK (reported_seconds >= 0 AND reported_seconds <= granted_seconds),
    CONSTRAINT chk_meeting_usage_reservation_status CHECK (status IN ('active', 'settled', 'released', 'expired')),
    CONSTRAINT chk_meeting_usage_reservation_finalized CHECK (
        (status = 'active' AND finalized_at IS NULL) OR
        (status <> 'active' AND finalized_at IS NOT NULL)
    )
);

CREATE INDEX idx_meeting_usage_reservation_active
    ON meeting_usage_reservations(user_id, period_start, expires_at)
    WHERE status = 'active';

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
    CONSTRAINT chk_meeting_usage_record_kind CHECK (usage_kind IN ('asr_audio')),
    CONSTRAINT chk_meeting_usage_record_actual CHECK (actual_seconds >= 0),
    CONSTRAINT chk_meeting_usage_record_provider CHECK (provider_usage_seconds >= 0),
    CONSTRAINT chk_meeting_usage_record_reason CHECK (
        settlement_reason IN ('completed', 'quota_exhausted', 'cancelled', 'failed', 'expired', 'preparation_failed')
    )
);

CREATE INDEX idx_meeting_usage_record_user_period
    ON meeting_usage_records(user_id, period_start, settled_at);

COMMIT;

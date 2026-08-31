BEGIN;

CREATE TABLE meeting_quota_policies (
    id SMALLINT PRIMARY KEY,
    monthly_audio_seconds BIGINT NOT NULL,
    max_meeting_audio_seconds BIGINT NOT NULL,
    max_concurrent_meetings INTEGER NOT NULL,
    create_rate_limit INTEGER NOT NULL,
    create_rate_window_seconds BIGINT NOT NULL,
    period_timezone VARCHAR(64) NOT NULL,
    usage_report_interval_seconds BIGINT NOT NULL,
    reservation_ttl_seconds BIGINT NOT NULL,
    redis_failure_policy VARCHAR(32) NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_meeting_quota_policy_singleton CHECK (id = 1),
    CONSTRAINT chk_meeting_quota_policy_monthly CHECK (monthly_audio_seconds BETWEEN 1 AND 31622400),
    CONSTRAINT chk_meeting_quota_policy_single CHECK (max_meeting_audio_seconds BETWEEN 1 AND 86400),
    CONSTRAINT chk_meeting_quota_policy_concurrency CHECK (max_concurrent_meetings BETWEEN 1 AND 1000),
    CONSTRAINT chk_meeting_quota_policy_rate_limit CHECK (create_rate_limit BETWEEN 1 AND 10000),
    CONSTRAINT chk_meeting_quota_policy_rate_window CHECK (create_rate_window_seconds BETWEEN 1 AND 86400),
    CONSTRAINT chk_meeting_quota_policy_timezone CHECK (period_timezone = 'Asia/Shanghai'),
    CONSTRAINT chk_meeting_quota_policy_report_interval CHECK (usage_report_interval_seconds BETWEEN 1 AND 3600),
    CONSTRAINT chk_meeting_quota_policy_reservation_ttl CHECK (reservation_ttl_seconds BETWEEN 1 AND 90000),
    CONSTRAINT chk_meeting_quota_policy_durations CHECK (
        usage_report_interval_seconds < reservation_ttl_seconds
        AND reservation_ttl_seconds >= max_meeting_audio_seconds
    ),
    CONSTRAINT chk_meeting_quota_policy_redis_failure CHECK (redis_failure_policy IN ('deny', 'postgres_fallback')),
    CONSTRAINT chk_meeting_quota_policy_version CHECK (version > 0)
);

INSERT INTO meeting_quota_policies (
    id,
    monthly_audio_seconds,
    max_meeting_audio_seconds,
    max_concurrent_meetings,
    create_rate_limit,
    create_rate_window_seconds,
    period_timezone,
    usage_report_interval_seconds,
    reservation_ttl_seconds,
    redis_failure_policy
) VALUES (
    1,
    7200,
    14400,
    1,
    5,
    600,
    'Asia/Shanghai',
    30,
    14430,
    'deny'
);

COMMIT;

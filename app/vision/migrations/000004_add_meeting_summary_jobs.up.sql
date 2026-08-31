BEGIN;

CREATE TABLE meeting_summary_jobs (
    id UUID PRIMARY KEY,
    meeting_id UUID NOT NULL,
    user_id UUID NOT NULL,
    version BIGINT NOT NULL,
    source_transcript_revision BIGINT NOT NULL,
    language VARCHAR(16) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    status VARCHAR(24) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    model_name VARCHAR(128) NOT NULL,
    prompt_version VARCHAR(64) NOT NULL,
    result_json JSONB,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    failure_reason VARCHAR(64) NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT uk_meeting_summary_job_version UNIQUE (meeting_id, version),
    CONSTRAINT uk_meeting_summary_job_idempotency UNIQUE (meeting_id, idempotency_key),
    CONSTRAINT chk_meeting_summary_job_version CHECK (version > 0),
    CONSTRAINT chk_meeting_summary_job_revision CHECK (source_transcript_revision > 0),
    CONSTRAINT chk_meeting_summary_job_language CHECK (language IN ('auto', 'zh-CN', 'en-US')),
    CONSTRAINT chk_meeting_summary_job_status CHECK (status IN ('pending', 'processing', 'delivery_pending', 'failure_delivery_pending', 'succeeded', 'failed')),
    CONSTRAINT chk_meeting_summary_job_usage CHECK (input_tokens >= 0 AND output_tokens >= 0 AND attempt_count >= 0),
    CONSTRAINT chk_meeting_summary_job_result CHECK (
        (status IN ('delivery_pending', 'succeeded') AND result_json IS NOT NULL AND failure_reason = '')
        OR (status IN ('failure_delivery_pending', 'failed') AND failure_reason <> '')
        OR status IN ('pending', 'processing')
    )
);

CREATE INDEX idx_meeting_summary_job_claim
    ON meeting_summary_jobs(status, available_at, created_at);

COMMIT;

BEGIN;

CREATE TABLE transcription_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    meeting_id UUID NOT NULL UNIQUE,
    user_id UUID NOT NULL,
    reservation_id UUID NOT NULL,
    language VARCHAR(16) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    provider VARCHAR(32) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL UNIQUE,
    granted_audio_milliseconds BIGINT NOT NULL,
    accepted_audio_bytes BIGINT NOT NULL DEFAULT 0,
    last_audio_sequence BIGINT NOT NULL DEFAULT 0,
    failure_code VARCHAR(64) NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_transcription_sessions_language
        CHECK (language IN ('auto', 'zh-CN', 'en-US')),
    CONSTRAINT ck_transcription_sessions_status
        CHECK (status IN ('pending', 'connecting', 'streaming', 'finishing', 'succeeded', 'failed', 'cancelled', 'expired')),
    CONSTRAINT ck_transcription_sessions_provider
        CHECK (provider IN ('bailian_paraformer')),
    CONSTRAINT ck_transcription_sessions_grant
        CHECK (granted_audio_milliseconds > 0 AND granted_audio_milliseconds <= 86400000),
    CONSTRAINT ck_transcription_sessions_usage
        CHECK (accepted_audio_bytes >= 0 AND last_audio_sequence >= 0),
    CONSTRAINT ck_transcription_sessions_failure
        CHECK ((status = 'failed' AND failure_code <> '') OR (status <> 'failed' AND failure_code = ''))
);

CREATE INDEX idx_transcription_sessions_user_created
    ON transcription_sessions (user_id, created_at DESC);
CREATE INDEX idx_transcription_sessions_status_updated
    ON transcription_sessions (status, updated_at);

CREATE TABLE transcription_audio_chunks (
    session_id UUID NOT NULL REFERENCES transcription_sessions (id) ON DELETE CASCADE,
    sequence_no BIGINT NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, sequence_no),
    CONSTRAINT ck_transcription_audio_chunks_sequence CHECK (sequence_no > 0),
    CONSTRAINT ck_transcription_audio_chunks_size CHECK (size_bytes > 0 AND size_bytes <= 65536)
);

CREATE TABLE asr_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL UNIQUE REFERENCES transcription_sessions (id) ON DELETE CASCADE,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    provider VARCHAR(32) NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_asr_jobs_status CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'cancelled')),
    CONSTRAINT ck_asr_jobs_provider CHECK (provider IN ('bailian_paraformer')),
    CONSTRAINT ck_asr_jobs_attempt_count CHECK (attempt_count >= 0)
);

CREATE INDEX idx_asr_jobs_status_created ON asr_jobs (status, created_at);

CREATE TABLE ai_job_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES asr_jobs (id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL,
    provider VARCHAR(32) NOT NULL,
    status VARCHAR(24) NOT NULL,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    CONSTRAINT uk_ai_job_attempts_number UNIQUE (job_id, attempt_number),
    CONSTRAINT ck_ai_job_attempts_number CHECK (attempt_number > 0),
    CONSTRAINT ck_ai_job_attempts_provider CHECK (provider IN ('bailian_paraformer')),
    CONSTRAINT ck_ai_job_attempts_status CHECK (status IN ('processing', 'succeeded', 'failed', 'cancelled'))
);

CREATE TABLE transcription_final_segments (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES transcription_sessions (id) ON DELETE CASCADE,
    sequence_no BIGINT NOT NULL,
    start_milliseconds BIGINT NOT NULL,
    end_milliseconds BIGINT NOT NULL,
    speaker_label VARCHAR(64) NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    language VARCHAR(16) NOT NULL,
    confidence DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_transcription_final_segments_sequence UNIQUE (session_id, sequence_no),
    CONSTRAINT ck_transcription_final_segments_sequence CHECK (sequence_no > 0),
    CONSTRAINT ck_transcription_final_segments_offsets
        CHECK (start_milliseconds >= 0 AND end_milliseconds >= start_milliseconds),
    CONSTRAINT ck_transcription_final_segments_language CHECK (language IN ('auto', 'zh-CN', 'en-US')),
    CONSTRAINT ck_transcription_final_segments_confidence CHECK (confidence BETWEEN 0 AND 1),
    CONSTRAINT ck_transcription_final_segments_content CHECK (length(btrim(content)) > 0)
);

CREATE TABLE transcription_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES transcription_sessions (id) ON DELETE CASCADE,
    event_type VARCHAR(48) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_transcription_outbox_event_type
        CHECK (event_type IN ('final_transcript_ready', 'transcription_usage_ready')),
    CONSTRAINT ck_transcription_outbox_status CHECK (status IN ('pending', 'processing', 'delivered', 'failed')),
    CONSTRAINT ck_transcription_outbox_attempts CHECK (attempt_count >= 0)
);

CREATE INDEX idx_transcription_outbox_delivery
    ON transcription_outbox (status, available_at, created_at);

COMMIT;

BEGIN;

ALTER TABLE meetings
    ADD COLUMN summary_status VARCHAR(24) NOT NULL DEFAULT 'not_started',
    ADD COLUMN summary_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT chk_meeting_summary_status CHECK (summary_status IN ('not_started', 'pending', 'processing', 'succeeded', 'failed')),
    ADD CONSTRAINT chk_meeting_summary_version CHECK (summary_version >= 0);

CREATE TABLE meeting_summaries (
    id UUID PRIMARY KEY,
    meeting_id UUID NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
    version BIGINT NOT NULL,
    source_transcript_revision BIGINT NOT NULL,
    idempotency_key VARCHAR(64) NOT NULL,
    status VARCHAR(24) NOT NULL,
    topic VARCHAR(300) NOT NULL DEFAULT '',
    abstract TEXT NOT NULL DEFAULT '',
    key_discussions JSONB NOT NULL DEFAULT '[]'::jsonb,
    decisions JSONB NOT NULL DEFAULT '[]'::jsonb,
    action_items JSONB NOT NULL DEFAULT '[]'::jsonb,
    risks JSONB NOT NULL DEFAULT '[]'::jsonb,
    provider VARCHAR(32) NOT NULL DEFAULT '',
    model_name VARCHAR(128) NOT NULL DEFAULT '',
    prompt_version VARCHAR(64) NOT NULL DEFAULT '',
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    failure_reason VARCHAR(64) NOT NULL DEFAULT '',
    generated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_meeting_summary_version UNIQUE (meeting_id, version),
    CONSTRAINT uk_meeting_summary_idempotency UNIQUE (meeting_id, idempotency_key),
    CONSTRAINT chk_meeting_summary_version_positive CHECK (version > 0),
    CONSTRAINT chk_meeting_summary_revision_positive CHECK (source_transcript_revision > 0),
    CONSTRAINT chk_meeting_summary_status CHECK (status IN ('pending', 'processing', 'succeeded', 'failed')),
    CONSTRAINT chk_meeting_summary_topic CHECK (char_length(topic) <= 300),
    CONSTRAINT chk_meeting_summary_abstract CHECK (char_length(abstract) <= 20000),
    CONSTRAINT chk_meeting_summary_arrays CHECK (
        jsonb_typeof(key_discussions) = 'array' AND jsonb_array_length(key_discussions) <= 100
        AND jsonb_typeof(decisions) = 'array' AND jsonb_array_length(decisions) <= 100
        AND jsonb_typeof(action_items) = 'array' AND jsonb_array_length(action_items) <= 100
        AND jsonb_typeof(risks) = 'array' AND jsonb_array_length(risks) <= 100
    ),
    CONSTRAINT chk_meeting_summary_tokens CHECK (input_tokens >= 0 AND output_tokens >= 0),
    CONSTRAINT chk_meeting_summary_terminal CHECK (
        (status = 'succeeded' AND topic <> '' AND abstract <> '' AND model_name <> '' AND prompt_version <> '' AND failure_reason = '' AND generated_at IS NOT NULL)
        OR (status = 'failed' AND failure_reason <> '' AND generated_at IS NULL)
        OR (status IN ('pending', 'processing') AND generated_at IS NULL)
    )
);

CREATE INDEX idx_meeting_summary_latest
    ON meeting_summaries(meeting_id, version DESC);

CREATE INDEX idx_meeting_summary_status
    ON meeting_summaries(status, updated_at);

COMMIT;

BEGIN;

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
    CONSTRAINT chk_meeting_summary_tokens CHECK (input_tokens >= 0 AND output_tokens >= 0)
);

INSERT INTO meeting_summaries (
    id, meeting_id, version, source_transcript_revision, idempotency_key,
    status, topic, abstract, key_discussions, decisions, action_items, risks,
    provider, model_name, prompt_version, input_tokens, output_tokens,
    failure_reason, generated_at, created_at, updated_at
)
SELECT
    gen_random_uuid(), id, summary_version, summary_source_transcript_revision,
    summary_idempotency_key, summary_status,
    COALESCE(summary_content ->> 'topic', ''),
    COALESCE(summary_content ->> 'abstract', ''),
    COALESCE(summary_content -> 'key_discussions', '[]'::jsonb),
    COALESCE(summary_content -> 'decisions', '[]'::jsonb),
    COALESCE(summary_content -> 'action_items', '[]'::jsonb),
    COALESCE(summary_content -> 'risks', '[]'::jsonb),
    summary_provider, summary_model_name, summary_prompt_version,
    summary_input_tokens, summary_output_tokens, summary_failure_reason,
    summary_generated_at, created_at, updated_at
FROM meetings
WHERE summary_version > 0;

CREATE INDEX idx_meeting_summary_latest
    ON meeting_summaries(meeting_id, version DESC);
CREATE INDEX idx_meeting_summary_status
    ON meeting_summaries(status, updated_at);

DROP INDEX idx_meetings_summary_idempotency;

ALTER TABLE meetings
    DROP CONSTRAINT chk_meeting_summary_compact_state,
    DROP CONSTRAINT chk_meeting_summary_tokens_nonnegative,
    DROP CONSTRAINT chk_meeting_summary_content_object,
    DROP CONSTRAINT chk_meeting_summary_source_revision,
    DROP COLUMN summary_generated_at,
    DROP COLUMN summary_failure_reason,
    DROP COLUMN summary_output_tokens,
    DROP COLUMN summary_input_tokens,
    DROP COLUMN summary_prompt_version,
    DROP COLUMN summary_model_name,
    DROP COLUMN summary_provider,
    DROP COLUMN summary_content,
    DROP COLUMN summary_idempotency_key,
    DROP COLUMN summary_source_transcript_revision;

COMMIT;

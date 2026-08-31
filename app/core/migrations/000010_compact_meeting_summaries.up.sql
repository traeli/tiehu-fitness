BEGIN;

ALTER TABLE meetings
    ADD COLUMN summary_source_transcript_revision BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN summary_idempotency_key VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN summary_content JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN summary_provider VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN summary_model_name VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN summary_prompt_version VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN summary_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN summary_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN summary_failure_reason VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN summary_generated_at TIMESTAMPTZ;

WITH latest AS (
    SELECT DISTINCT ON (meeting_id)
        meeting_id, version, source_transcript_revision, idempotency_key,
        status, topic, abstract, key_discussions, decisions, action_items,
        risks, provider, model_name, prompt_version, input_tokens,
        output_tokens, failure_reason, generated_at
    FROM meeting_summaries
    ORDER BY meeting_id, version DESC
)
UPDATE meetings AS m
SET summary_status = latest.status,
    summary_version = latest.version,
    summary_source_transcript_revision = latest.source_transcript_revision,
    summary_idempotency_key = latest.idempotency_key,
    summary_content = jsonb_build_object(
        'topic', latest.topic,
        'abstract', latest.abstract,
        'key_discussions', latest.key_discussions,
        'decisions', latest.decisions,
        'action_items', latest.action_items,
        'risks', latest.risks
    ),
    summary_provider = latest.provider,
    summary_model_name = latest.model_name,
    summary_prompt_version = latest.prompt_version,
    summary_input_tokens = latest.input_tokens,
    summary_output_tokens = latest.output_tokens,
    summary_failure_reason = latest.failure_reason,
    summary_generated_at = latest.generated_at
FROM latest
WHERE m.id = latest.meeting_id;

DROP TABLE meeting_summaries;

ALTER TABLE meetings
    ADD CONSTRAINT chk_meeting_summary_source_revision
        CHECK (summary_source_transcript_revision >= 0),
    ADD CONSTRAINT chk_meeting_summary_content_object
        CHECK (jsonb_typeof(summary_content) = 'object'),
    ADD CONSTRAINT chk_meeting_summary_tokens_nonnegative
        CHECK (summary_input_tokens >= 0 AND summary_output_tokens >= 0),
    ADD CONSTRAINT chk_meeting_summary_compact_state CHECK (
        (summary_status = 'not_started'
            AND summary_version = 0
            AND summary_source_transcript_revision = 0
            AND summary_idempotency_key = '')
        OR (summary_status IN ('pending', 'processing')
            AND summary_version > 0
            AND summary_source_transcript_revision > 0
            AND summary_idempotency_key <> ''
            AND summary_generated_at IS NULL)
        OR (summary_status = 'succeeded'
            AND summary_version > 0
            AND summary_source_transcript_revision > 0
            AND summary_idempotency_key <> ''
            AND summary_provider <> ''
            AND summary_model_name <> ''
            AND summary_prompt_version <> ''
            AND summary_failure_reason = ''
            AND summary_generated_at IS NOT NULL)
        OR (summary_status = 'failed'
            AND summary_version > 0
            AND summary_source_transcript_revision > 0
            AND summary_idempotency_key <> ''
            AND summary_failure_reason <> ''
            AND summary_generated_at IS NULL)
    );

CREATE INDEX idx_meetings_summary_idempotency
    ON meetings(id, summary_idempotency_key)
    WHERE summary_version > 0;

COMMIT;

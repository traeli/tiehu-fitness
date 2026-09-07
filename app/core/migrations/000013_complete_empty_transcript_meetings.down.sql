BEGIN;

UPDATE meetings
SET status = 'processing',
    summary_status = 'not_started',
    summary_version = 0,
    summary_source_transcript_revision = 0,
    summary_idempotency_key = '',
    summary_content = '{}'::jsonb,
    summary_provider = '',
    summary_model_name = '',
    summary_prompt_version = '',
    summary_input_tokens = 0,
    summary_output_tokens = 0,
    summary_failure_reason = '',
    summary_generated_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE status = 'completed'
  AND transcription_status = 'succeeded'
  AND transcript_revision = 0
  AND summary_status = 'succeeded'
  AND summary_version = 1
  AND summary_provider = 'system'
  AND summary_model_name = 'empty-transcript-fallback'
  AND summary_prompt_version = 'empty-transcript-v1';

ALTER TABLE meetings
    DROP CONSTRAINT IF EXISTS chk_meeting_summary_compact_state,
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

COMMIT;

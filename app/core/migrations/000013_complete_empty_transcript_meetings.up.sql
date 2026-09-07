BEGIN;

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
            AND summary_source_transcript_revision >= 0
            AND summary_idempotency_key <> ''
            AND summary_provider <> ''
            AND summary_model_name <> ''
            AND summary_prompt_version <> ''
            AND summary_failure_reason = ''
            AND summary_generated_at IS NOT NULL
            AND (
                summary_source_transcript_revision > 0
                OR (
                    summary_provider = 'system'
                    AND summary_model_name = 'empty-transcript-fallback'
                    AND summary_prompt_version = 'empty-transcript-v1'
                )
            ))
        OR (summary_status = 'failed'
            AND summary_version > 0
            AND summary_source_transcript_revision > 0
            AND summary_idempotency_key <> ''
            AND summary_failure_reason <> ''
            AND summary_generated_at IS NULL)
    );

UPDATE meetings
SET status = 'completed',
    summary_status = 'succeeded',
    summary_version = 1,
    summary_source_transcript_revision = 0,
    summary_idempotency_key = 'automatic:empty-transcript',
    summary_content = '{"topic":"未识别到有效转写","abstract":"本次录音未识别到有效语音内容，已按实际录音时长完成额度结算。","key_discussions":[],"decisions":[],"action_items":[],"risks":[]}'::jsonb,
    summary_provider = 'system',
    summary_model_name = 'empty-transcript-fallback',
    summary_prompt_version = 'empty-transcript-v1',
    summary_input_tokens = 0,
    summary_output_tokens = 0,
    summary_failure_reason = '',
    summary_generated_at = COALESCE(stopped_at, updated_at, created_at, CURRENT_TIMESTAMP),
    updated_at = CURRENT_TIMESTAMP
WHERE status = 'processing'
  AND transcription_status = 'succeeded'
  AND transcript_revision = 0
  AND summary_status = 'not_started'
  AND summary_version = 0;

COMMIT;

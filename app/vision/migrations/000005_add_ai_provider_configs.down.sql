BEGIN;

DROP INDEX IF EXISTS idx_meeting_summary_jobs_provider_config_id;
ALTER TABLE meeting_summary_jobs DROP CONSTRAINT IF EXISTS fk_meeting_summary_jobs_provider_config;
ALTER TABLE meeting_summary_jobs DROP COLUMN IF EXISTS provider_config_id;

DROP INDEX IF EXISTS idx_transcription_sessions_provider_config_id;
ALTER TABLE transcription_sessions DROP CONSTRAINT IF EXISTS fk_transcription_sessions_provider_config;
ALTER TABLE transcription_sessions DROP COLUMN IF EXISTS provider_config_id;

DROP TABLE IF EXISTS meeting_summary_provider_configs;
DROP TABLE IF EXISTS asr_provider_configs;

COMMIT;

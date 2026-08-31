BEGIN;

ALTER TABLE transcription_sessions DROP CONSTRAINT ck_transcription_sessions_provider;
ALTER TABLE transcription_sessions
    ADD CONSTRAINT ck_transcription_sessions_provider
    CHECK (provider IN ('bailian_paraformer', 'local_fake'));

ALTER TABLE asr_jobs DROP CONSTRAINT ck_asr_jobs_provider;
ALTER TABLE asr_jobs
    ADD CONSTRAINT ck_asr_jobs_provider
    CHECK (provider IN ('bailian_paraformer', 'local_fake'));

ALTER TABLE ai_job_attempts DROP CONSTRAINT ck_ai_job_attempts_provider;
ALTER TABLE ai_job_attempts
    ADD CONSTRAINT ck_ai_job_attempts_provider
    CHECK (provider IN ('bailian_paraformer', 'local_fake'));

COMMIT;

BEGIN;

DROP TABLE IF EXISTS transcription_outbox;
DROP TABLE IF EXISTS transcription_final_segments;
DROP TABLE IF EXISTS ai_job_attempts;
DROP TABLE IF EXISTS asr_jobs;
DROP TABLE IF EXISTS transcription_audio_chunks;
DROP TABLE IF EXISTS transcription_sessions;

COMMIT;

BEGIN;

ALTER TABLE meeting_summary_jobs
    DROP CONSTRAINT chk_meeting_summary_job_llm_failure_size,
    DROP CONSTRAINT chk_meeting_summary_job_llm_duration,
    DROP CONSTRAINT chk_meeting_summary_job_llm_http_status,
    DROP CONSTRAINT chk_meeting_summary_job_llm_payload_size,
    DROP COLUMN llm_failure,
    DROP COLUMN llm_duration_milliseconds,
    DROP COLUMN llm_http_status,
    DROP COLUMN llm_response,
    DROP COLUMN llm_request;

COMMIT;

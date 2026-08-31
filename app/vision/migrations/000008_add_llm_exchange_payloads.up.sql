BEGIN;

ALTER TABLE meeting_summary_jobs
    ADD COLUMN llm_request TEXT NOT NULL DEFAULT '',
    ADD COLUMN llm_response TEXT NOT NULL DEFAULT '',
    ADD COLUMN llm_http_status INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN llm_duration_milliseconds BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN llm_failure TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT chk_meeting_summary_job_llm_payload_size CHECK (
        octet_length(llm_request) <= 2097152
        AND octet_length(llm_response) <= 2097152
    ),
    ADD CONSTRAINT chk_meeting_summary_job_llm_http_status CHECK (
        llm_http_status BETWEEN 0 AND 599
    ),
    ADD CONSTRAINT chk_meeting_summary_job_llm_duration CHECK (
        llm_duration_milliseconds >= 0
    ),
    ADD CONSTRAINT chk_meeting_summary_job_llm_failure_size CHECK (
        char_length(llm_failure) <= 2000
    );

COMMIT;

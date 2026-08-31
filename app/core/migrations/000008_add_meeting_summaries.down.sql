BEGIN;

DROP TABLE IF EXISTS meeting_summaries;

ALTER TABLE meetings
    DROP CONSTRAINT IF EXISTS chk_meeting_summary_version,
    DROP CONSTRAINT IF EXISTS chk_meeting_summary_status,
    DROP COLUMN IF EXISTS summary_version,
    DROP COLUMN IF EXISTS summary_status;

COMMIT;

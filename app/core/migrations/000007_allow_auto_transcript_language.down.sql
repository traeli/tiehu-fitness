BEGIN;

ALTER TABLE meeting_transcript_segments
    DROP CONSTRAINT chk_meeting_segment_language;

ALTER TABLE meeting_transcript_segments
    ADD CONSTRAINT chk_meeting_segment_language
        CHECK (language IN ('zh_cn', 'en_us'));

COMMIT;

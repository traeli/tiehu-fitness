BEGIN;

ALTER TABLE meeting_quota_policies
    DROP CONSTRAINT IF EXISTS chk_meeting_quota_policy_durations;

UPDATE meeting_quota_policies
SET reservation_ttl_seconds = GREATEST(
        max_meeting_audio_seconds,
        usage_report_interval_seconds + 1
    ),
    version = version + 1,
    updated_at = NOW()
WHERE id = 1;

ALTER TABLE meeting_quota_policies
    ADD CONSTRAINT chk_meeting_quota_policy_durations CHECK (
        usage_report_interval_seconds < reservation_ttl_seconds
        AND reservation_ttl_seconds >= max_meeting_audio_seconds
    );

COMMIT;

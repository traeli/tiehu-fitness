BEGIN;

ALTER TABLE meeting_quota_policies
    DROP CONSTRAINT IF EXISTS chk_meeting_quota_policy_durations;

-- Replace the original maximum-meeting reservation with a short renewable
-- lease. Preserve quota limits and other operator customizations.
UPDATE meeting_quota_policies
SET usage_report_interval_seconds = 15,
    reservation_ttl_seconds = 120,
    version = version + 1,
    updated_at = NOW()
WHERE id = 1
  AND (usage_report_interval_seconds IS DISTINCT FROM 15
       OR reservation_ttl_seconds IS DISTINCT FROM 120);

UPDATE meeting_quota_policies
SET reservation_ttl_seconds = usage_report_interval_seconds * 2,
    version = version + 1,
    updated_at = NOW()
WHERE reservation_ttl_seconds < usage_report_interval_seconds * 2;

ALTER TABLE meeting_quota_policies
    ADD CONSTRAINT chk_meeting_quota_policy_durations CHECK (
        reservation_ttl_seconds >= usage_report_interval_seconds * 2
    );

-- Bound reservations created by the previous four-hour policy. A healthy
-- Vision connection renews them on its next heartbeat; abandoned meetings are
-- recovered by the reconciler instead of retaining the old deadline.
UPDATE meetings
SET quota_expires_at = LEAST(quota_expires_at, NOW() + INTERVAL '120 seconds'),
    updated_at = NOW()
WHERE quota_status = 'active'
  AND quota_expires_at > NOW() + INTERVAL '120 seconds';

COMMIT;

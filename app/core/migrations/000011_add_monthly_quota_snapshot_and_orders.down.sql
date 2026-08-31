BEGIN;

DROP TABLE IF EXISTS orders;

ALTER TABLE meeting_usage_periods
    DROP CONSTRAINT IF EXISTS uk_meeting_usage_period_id_user,
    DROP CONSTRAINT IF EXISTS chk_meeting_usage_period_purchased_quota,
    DROP CONSTRAINT IF EXISTS chk_meeting_usage_period_base_quota,
    DROP COLUMN IF EXISTS purchased_quota_seconds,
    DROP COLUMN IF EXISTS base_quota_seconds;

COMMIT;

BEGIN;

DROP TABLE IF EXISTS meeting_usage_records;
DROP TABLE IF EXISTS meeting_usage_reservations;
DROP TABLE IF EXISTS meeting_usage_periods;
DROP TABLE IF EXISTS user_meeting_quota_overrides;

COMMIT;

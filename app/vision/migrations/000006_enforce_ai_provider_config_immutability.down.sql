BEGIN;

DROP TRIGGER IF EXISTS trg_meeting_summary_provider_configs_lifecycle
    ON meeting_summary_provider_configs;
DROP TRIGGER IF EXISTS trg_asr_provider_configs_lifecycle
    ON asr_provider_configs;
DROP FUNCTION IF EXISTS enforce_ai_provider_config_lifecycle();

COMMIT;

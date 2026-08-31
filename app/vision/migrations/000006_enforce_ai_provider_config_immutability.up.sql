BEGIN;

CREATE FUNCTION enforce_ai_provider_config_lifecycle()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status = 'draft' AND NEW.status NOT IN ('draft', 'active', 'retired') THEN
        RAISE EXCEPTION 'invalid AI provider config transition from draft to %', NEW.status;
    END IF;
    IF OLD.status = 'active' AND NEW.status NOT IN ('active', 'retired') THEN
        RAISE EXCEPTION 'invalid AI provider config transition from active to %', NEW.status;
    END IF;
    IF OLD.status = 'retired' AND NEW.status <> 'retired' THEN
        RAISE EXCEPTION 'retired AI provider config cannot transition to %', NEW.status;
    END IF;

    IF OLD.status IN ('active', 'retired')
       AND (to_jsonb(NEW) - 'status' - 'updated_at')
           IS DISTINCT FROM (to_jsonb(OLD) - 'status' - 'updated_at') THEN
        RAISE EXCEPTION 'active or retired AI provider config is immutable';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_asr_provider_configs_lifecycle
BEFORE UPDATE ON asr_provider_configs
FOR EACH ROW EXECUTE FUNCTION enforce_ai_provider_config_lifecycle();

CREATE TRIGGER trg_meeting_summary_provider_configs_lifecycle
BEFORE UPDATE ON meeting_summary_provider_configs
FOR EACH ROW EXECUTE FUNCTION enforce_ai_provider_config_lifecycle();

COMMIT;

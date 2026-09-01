BEGIN;

SELECT pg_advisory_xact_lock(hashtext('vision:summary-provider-config'));

DO $$
DECLARE
    active_config meeting_summary_provider_configs%ROWTYPE;
    next_version BIGINT;
BEGIN
    SELECT * INTO active_config
    FROM meeting_summary_provider_configs
    WHERE status = 'active'
    FOR UPDATE;

    IF NOT FOUND OR active_config.prompt_version <> 'meeting-summary-v2' THEN
        RETURN;
    END IF;

    SELECT COALESCE(MAX(version), 0) + 1 INTO next_version
    FROM meeting_summary_provider_configs;

    UPDATE meeting_summary_provider_configs
    SET status = 'retired', updated_at = now()
    WHERE id = active_config.id;

    INSERT INTO meeting_summary_provider_configs (
        id, version, status, provider, model_name, prompt_version,
        max_input_chars_per_chunk, max_chunks, max_output_tokens,
        activated_at, created_at, updated_at
    ) VALUES (
        gen_random_uuid(), next_version, 'active', active_config.provider,
        active_config.model_name, 'meeting-summary-v1',
        active_config.max_input_chars_per_chunk, active_config.max_chunks,
        active_config.max_output_tokens, now(), now(), now()
    );
END;
$$;

COMMIT;

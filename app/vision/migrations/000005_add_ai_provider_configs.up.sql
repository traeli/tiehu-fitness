BEGIN;

CREATE TABLE asr_provider_configs (
    id UUID PRIMARY KEY,
    version BIGINT NOT NULL UNIQUE,
    status VARCHAR(16) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    workspace_id VARCHAR(63) NOT NULL,
    realtime_model VARCHAR(128) NOT NULL,
    file_model VARCHAR(128) NOT NULL,
    vocabulary_id VARCHAR(128) NOT NULL DEFAULT '',
    activated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_asr_provider_configs_version CHECK (version > 0),
    CONSTRAINT ck_asr_provider_configs_status CHECK (status IN ('draft', 'active', 'retired')),
    CONSTRAINT ck_asr_provider_configs_provider CHECK (provider IN ('bailian_paraformer')),
    CONSTRAINT ck_asr_provider_configs_workspace CHECK (workspace_id ~ '^[A-Za-z0-9][A-Za-z0-9-]{0,62}$'),
    CONSTRAINT ck_asr_provider_configs_realtime_model CHECK (realtime_model ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
    CONSTRAINT ck_asr_provider_configs_file_model CHECK (file_model ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
    CONSTRAINT ck_asr_provider_configs_vocabulary CHECK (vocabulary_id = '' OR vocabulary_id ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
    CONSTRAINT ck_asr_provider_configs_activation CHECK (
        (status = 'draft' AND activated_at IS NULL)
        OR (status IN ('active', 'retired') AND activated_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX uk_asr_provider_configs_one_active
    ON asr_provider_configs ((status)) WHERE status = 'active';

CREATE TABLE meeting_summary_provider_configs (
    id UUID PRIMARY KEY,
    version BIGINT NOT NULL UNIQUE,
    status VARCHAR(16) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    model_name VARCHAR(128) NOT NULL,
    prompt_version VARCHAR(64) NOT NULL,
    max_input_chars_per_chunk INTEGER NOT NULL,
    max_chunks INTEGER NOT NULL,
    max_output_tokens INTEGER NOT NULL,
    activated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_meeting_summary_provider_configs_version CHECK (version > 0),
    CONSTRAINT ck_meeting_summary_provider_configs_status CHECK (status IN ('draft', 'active', 'retired')),
    CONSTRAINT ck_meeting_summary_provider_configs_provider CHECK (provider IN ('deepseek')),
    CONSTRAINT ck_meeting_summary_provider_configs_model CHECK (model_name ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
    CONSTRAINT ck_meeting_summary_provider_configs_prompt CHECK (prompt_version ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
    CONSTRAINT ck_meeting_summary_provider_configs_bounds CHECK (
        max_input_chars_per_chunk BETWEEN 1000 AND 500000
        AND max_chunks BETWEEN 1 AND 128
        AND max_output_tokens BETWEEN 256 AND 100000
    ),
    CONSTRAINT ck_meeting_summary_provider_configs_activation CHECK (
        (status = 'draft' AND activated_at IS NULL)
        OR (status IN ('active', 'retired') AND activated_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX uk_meeting_summary_provider_configs_one_active
    ON meeting_summary_provider_configs ((status)) WHERE status = 'active';

INSERT INTO asr_provider_configs (
    id, version, status, provider, workspace_id, realtime_model, file_model, vocabulary_id, activated_at
) VALUES (
    '10000000-0000-4000-8000-000000000001', 1, 'active', 'bailian_paraformer',
    'ws-ow92dz82zf0fph67', 'paraformer-realtime-v2', 'paraformer-v2', '', now()
);

INSERT INTO meeting_summary_provider_configs (
    id, version, status, provider, model_name, prompt_version,
    max_input_chars_per_chunk, max_chunks, max_output_tokens, activated_at
) VALUES (
    '20000000-0000-4000-8000-000000000001', 1, 'active', 'deepseek', 'deepseek-v4-flash',
    'meeting-summary-v1', 60000, 64, 8192, now()
);

ALTER TABLE transcription_sessions ADD COLUMN provider_config_id UUID;
UPDATE transcription_sessions
SET provider_config_id = '10000000-0000-4000-8000-000000000001'
WHERE provider_config_id IS NULL;
ALTER TABLE transcription_sessions ALTER COLUMN provider_config_id SET NOT NULL;
ALTER TABLE transcription_sessions
    ADD CONSTRAINT fk_transcription_sessions_provider_config
    FOREIGN KEY (provider_config_id) REFERENCES asr_provider_configs (id) ON DELETE RESTRICT;
CREATE INDEX idx_transcription_sessions_provider_config_id
    ON transcription_sessions (provider_config_id);

ALTER TABLE meeting_summary_jobs ADD COLUMN provider_config_id UUID;
UPDATE meeting_summary_jobs
SET provider_config_id = '20000000-0000-4000-8000-000000000001'
WHERE provider_config_id IS NULL;
ALTER TABLE meeting_summary_jobs ALTER COLUMN provider_config_id SET NOT NULL;
ALTER TABLE meeting_summary_jobs
    ADD CONSTRAINT fk_meeting_summary_jobs_provider_config
    FOREIGN KEY (provider_config_id) REFERENCES meeting_summary_provider_configs (id) ON DELETE RESTRICT;
CREATE INDEX idx_meeting_summary_jobs_provider_config_id
    ON meeting_summary_jobs (provider_config_id);

COMMIT;

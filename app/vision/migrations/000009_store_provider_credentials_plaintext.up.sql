BEGIN;

-- Existing ciphertext cannot be converted without the machine-local key.
-- Recreate the two credentials with `make configure-vision-credentials`.
DROP TABLE provider_credentials;

CREATE TABLE provider_credentials (
    provider VARCHAR(32) PRIMARY KEY,
    api_key TEXT NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_provider_credentials_provider
        CHECK (provider IN ('bailian_paraformer', 'deepseek')),
    CONSTRAINT ck_provider_credentials_api_key
        CHECK (char_length(api_key) BETWEEN 1 AND 4096 AND api_key = btrim(api_key)),
    CONSTRAINT ck_provider_credentials_version CHECK (version > 0)
);

COMMIT;

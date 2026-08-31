BEGIN;

CREATE TABLE provider_credentials (
    provider VARCHAR(32) PRIMARY KEY,
    api_key_ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_provider_credentials_provider
        CHECK (provider IN ('bailian_paraformer', 'deepseek')),
    CONSTRAINT ck_provider_credentials_ciphertext
        CHECK (octet_length(api_key_ciphertext) BETWEEN 17 AND 4112),
    CONSTRAINT ck_provider_credentials_nonce CHECK (octet_length(nonce) = 12),
    CONSTRAINT ck_provider_credentials_version CHECK (version > 0)
);

COMMIT;

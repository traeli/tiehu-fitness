BEGIN;

CREATE TABLE password_credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    username VARCHAR(64) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_password_credentials_username_length
        CHECK (char_length(username) BETWEEN 3 AND 64),
    CONSTRAINT ck_password_credentials_username_normalized
        CHECK (username = lower(username)),
    CONSTRAINT ck_password_credentials_hash_not_empty
        CHECK (length(password_hash) > 0)
);

COMMIT;

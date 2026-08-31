BEGIN;

ALTER TABLE password_credentials
    DROP CONSTRAINT ck_password_credentials_email_length,
    DROP CONSTRAINT ck_password_credentials_email_normalized,
    DROP CONSTRAINT ck_password_credentials_email_shape;

ALTER TABLE password_credentials
    ALTER COLUMN email TYPE VARCHAR(64);

ALTER TABLE password_credentials
    RENAME CONSTRAINT password_credentials_email_key
    TO password_credentials_username_key;

ALTER TABLE password_credentials
    RENAME COLUMN email TO username;

ALTER TABLE password_credentials
    ADD CONSTRAINT ck_password_credentials_username_length
        CHECK (char_length(username) BETWEEN 3 AND 64),
    ADD CONSTRAINT ck_password_credentials_username_normalized
        CHECK (username = lower(username));

COMMIT;

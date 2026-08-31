BEGIN;

ALTER TABLE password_credentials
    RENAME COLUMN username TO email;

ALTER TABLE password_credentials
    DROP CONSTRAINT ck_password_credentials_username_length,
    DROP CONSTRAINT ck_password_credentials_username_normalized;

ALTER TABLE password_credentials
    ALTER COLUMN email TYPE VARCHAR(254);

ALTER TABLE password_credentials
    RENAME CONSTRAINT password_credentials_username_key
    TO password_credentials_email_key;

ALTER TABLE password_credentials
    ADD CONSTRAINT ck_password_credentials_email_length
        CHECK (char_length(email) BETWEEN 3 AND 254),
    ADD CONSTRAINT ck_password_credentials_email_normalized
        CHECK (email = lower(email)),
    ADD CONSTRAINT ck_password_credentials_email_shape
        CHECK (email ~ '^[^@[:space:]]+@[^@[:space:]]+$');

COMMIT;

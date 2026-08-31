BEGIN;

CREATE TABLE utools_identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    plugin_id VARCHAR(64) NOT NULL,
    open_id VARCHAR(128) NOT NULL,
    member BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_utools_plugin_openid UNIQUE (plugin_id, open_id),
    CONSTRAINT ck_utools_identities_plugin_id_not_empty CHECK (length(plugin_id) > 0),
    CONSTRAINT ck_utools_identities_open_id_not_empty CHECK (length(open_id) > 0)
);

CREATE INDEX idx_utools_identities_user_id ON utools_identities (user_id);

COMMIT;

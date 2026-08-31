BEGIN;

ALTER TABLE meeting_usage_periods
    ADD COLUMN base_quota_seconds BIGINT,
    ADD COLUMN purchased_quota_seconds BIGINT NOT NULL DEFAULT 0;

UPDATE meeting_usage_periods AS usage_period
SET base_quota_seconds = COALESCE(
    (
        SELECT quota_override.monthly_audio_seconds
        FROM user_meeting_quota_overrides AS quota_override
        WHERE quota_override.user_id = usage_period.user_id
          AND quota_override.status = 'active'
          AND quota_override.monthly_audio_seconds IS NOT NULL
    ),
    (
        SELECT quota_policy.monthly_audio_seconds
        FROM meeting_quota_policies AS quota_policy
        WHERE quota_policy.id = 1
    )
)
WHERE usage_period.base_quota_seconds IS NULL;

ALTER TABLE meeting_usage_periods
    ALTER COLUMN base_quota_seconds SET NOT NULL,
    ADD CONSTRAINT uk_meeting_usage_period_id_user UNIQUE (id, user_id),
    ADD CONSTRAINT chk_meeting_usage_period_base_quota CHECK (base_quota_seconds > 0),
    ADD CONSTRAINT chk_meeting_usage_period_purchased_quota CHECK (purchased_quota_seconds >= 0);

CREATE TABLE orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    usage_period_id UUID NOT NULL,
    type VARCHAR(32) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    external_order_id VARCHAR(128),
    purchased_seconds BIGINT NOT NULL,
    paid_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_orders_external_order_id UNIQUE (external_order_id),
    CONSTRAINT fk_orders_usage_period_owner FOREIGN KEY (usage_period_id, user_id)
        REFERENCES meeting_usage_periods(id, user_id) ON DELETE CASCADE,
    CONSTRAINT chk_orders_type CHECK (type IN ('meeting_quota')),
    CONSTRAINT chk_orders_status CHECK (status IN ('pending', 'paid', 'cancelled', 'refunded')),
    CONSTRAINT chk_orders_purchased_seconds CHECK (purchased_seconds > 0),
    CONSTRAINT chk_orders_paid_at CHECK (
        (status IN ('pending', 'cancelled') AND paid_at IS NULL) OR
        (status IN ('paid', 'refunded') AND paid_at IS NOT NULL)
    )
);

CREATE INDEX idx_orders_user_created_at ON orders(user_id, created_at DESC);
CREATE INDEX idx_orders_usage_period ON orders(usage_period_id, created_at DESC);
CREATE INDEX idx_orders_status_created_at ON orders(status, created_at);

COMMIT;

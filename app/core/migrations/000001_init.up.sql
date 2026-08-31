BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nickname VARCHAR(80) NOT NULL DEFAULT '',
    avatar_uri TEXT NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT ck_users_status CHECK (status IN ('active', 'disabled'))
);

CREATE INDEX idx_users_status ON users (status) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_deleted_at ON users (deleted_at);

CREATE TABLE wechat_identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    app_id VARCHAR(64) NOT NULL,
    open_id VARCHAR(128) NOT NULL,
    union_id VARCHAR(128) NOT NULL DEFAULT '',
    session_key_ciphertext BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_wechat_app_openid UNIQUE (app_id, open_id)
);

CREATE INDEX idx_wechat_identities_user_id ON wechat_identities (user_id);
CREATE INDEX idx_wechat_identities_union_id
    ON wechat_identities (union_id)
    WHERE union_id <> '';

CREATE TABLE user_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_id VARCHAR(128) NOT NULL DEFAULT '',
    access_token_hash VARCHAR(128) NOT NULL UNIQUE,
    refresh_token_hash VARCHAR(128) NOT NULL UNIQUE,
    access_expires_at TIMESTAMPTZ NOT NULL,
    refresh_expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_user_sessions_user_id ON user_sessions (user_id);
CREATE INDEX idx_user_sessions_access_expires_at ON user_sessions (access_expires_at);
CREATE INDEX idx_user_sessions_refresh_expires_at ON user_sessions (refresh_expires_at);
CREATE INDEX idx_user_sessions_revoked_at ON user_sessions (revoked_at);

CREATE TABLE fitness_profiles (
    user_id UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    goal VARCHAR(32) NOT NULL DEFAULT '',
    experience_level VARCHAR(32) NOT NULL DEFAULT '',
    days_per_week SMALLINT NOT NULL DEFAULT 0,
    duration_minutes SMALLINT NOT NULL DEFAULT 0,
    available_equipment_codes JSONB NOT NULL DEFAULT '[]'::jsonb,
    injury_notes JSONB NOT NULL DEFAULT '[]'::jsonb,
    onboarding_completed BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_fitness_profiles_days CHECK (days_per_week BETWEEN 0 AND 7),
    CONSTRAINT ck_fitness_profiles_duration CHECK (duration_minutes >= 0)
);

CREATE TABLE equipment (
    code VARCHAR(64) PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    target_muscles JSONB NOT NULL DEFAULT '[]'::jsonb,
    safety_tips JSONB NOT NULL DEFAULT '[]'::jsonb,
    status VARCHAR(24) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_equipment_status CHECK (status IN ('draft', 'published', 'archived'))
);

CREATE INDEX idx_equipment_status ON equipment (status);

CREATE TABLE exercises (
    code VARCHAR(64) PRIMARY KEY,
    equipment_code VARCHAR(64) NOT NULL REFERENCES equipment (code) ON DELETE RESTRICT,
    name VARCHAR(120) NOT NULL,
    instruction_video_uri TEXT NOT NULL DEFAULT '',
    target_muscles JSONB NOT NULL DEFAULT '[]'::jsonb,
    key_points JSONB NOT NULL DEFAULT '[]'::jsonb,
    common_mistakes JSONB NOT NULL DEFAULT '[]'::jsonb,
    status VARCHAR(24) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_exercises_status CHECK (status IN ('draft', 'published', 'archived'))
);

CREATE INDEX idx_exercises_equipment_code ON exercises (equipment_code);
CREATE INDEX idx_exercises_status ON exercises (status);

CREATE TABLE training_plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    goal VARCHAR(32) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'draft',
    starts_on DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_training_plans_status CHECK (status IN ('draft', 'active', 'completed', 'cancelled'))
);

CREATE INDEX idx_training_plans_user_id ON training_plans (user_id);
CREATE INDEX idx_training_plans_status ON training_plans (status);

CREATE TABLE training_plan_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES training_plans (id) ON DELETE CASCADE,
    day_number SMALLINT NOT NULL,
    exercise_code VARCHAR(64) NOT NULL REFERENCES exercises (code) ON DELETE RESTRICT,
    sets SMALLINT NOT NULL DEFAULT 0,
    reps SMALLINT NOT NULL DEFAULT 0,
    weight_kg NUMERIC(8,2) NOT NULL DEFAULT 0,
    sort_order SMALLINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_plan_day_order UNIQUE (plan_id, day_number, sort_order),
    CONSTRAINT ck_training_plan_items_day CHECK (day_number > 0),
    CONSTRAINT ck_training_plan_items_sets CHECK (sets >= 0),
    CONSTRAINT ck_training_plan_items_reps CHECK (reps >= 0),
    CONSTRAINT ck_training_plan_items_weight CHECK (weight_kg >= 0)
);

CREATE INDEX idx_training_plan_items_exercise_code ON training_plan_items (exercise_code);

CREATE TABLE workout_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    plan_id UUID REFERENCES training_plans (id) ON DELETE SET NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'in_progress',
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_workout_sessions_status CHECK (status IN ('in_progress', 'completed', 'cancelled')),
    CONSTRAINT ck_workout_sessions_duration CHECK (duration_seconds >= 0)
);

CREATE INDEX idx_workout_sessions_user_id ON workout_sessions (user_id);
CREATE INDEX idx_workout_sessions_plan_id ON workout_sessions (plan_id);
CREATE INDEX idx_workout_sessions_status ON workout_sessions (status);
CREATE INDEX idx_workout_sessions_started_at ON workout_sessions (started_at DESC);

CREATE TABLE workout_sets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES workout_sessions (id) ON DELETE CASCADE,
    exercise_code VARCHAR(64) NOT NULL REFERENCES exercises (code) ON DELETE RESTRICT,
    set_number SMALLINT NOT NULL,
    reps SMALLINT NOT NULL DEFAULT 0,
    weight_kg NUMERIC(8,2) NOT NULL DEFAULT 0,
    rpe NUMERIC(3,1),
    rest_seconds INTEGER NOT NULL DEFAULT 0,
    completed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_session_exercise_set UNIQUE (session_id, exercise_code, set_number),
    CONSTRAINT ck_workout_sets_number CHECK (set_number > 0),
    CONSTRAINT ck_workout_sets_reps CHECK (reps >= 0),
    CONSTRAINT ck_workout_sets_weight CHECK (weight_kg >= 0),
    CONSTRAINT ck_workout_sets_rpe CHECK (rpe IS NULL OR rpe BETWEEN 0 AND 10),
    CONSTRAINT ck_workout_sets_rest CHECK (rest_seconds >= 0)
);

CREATE TABLE check_ins (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    check_date DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_user_checkin_date UNIQUE (user_id, check_date)
);

INSERT INTO equipment (
    code, name, description, target_muscles, safety_tips, status
) VALUES (
    'cable-machine',
    '龙门架',
    '支持多方向绳索阻力训练',
    '["胸部", "背部", "肩部", "手臂"]'::jsonb,
    '["训练前检查插销", "保持钢索路径无遮挡"]'::jsonb,
    'published'
);

INSERT INTO exercises (
    code, equipment_code, name, instruction_video_uri,
    target_muscles, key_points, common_mistakes, status
) VALUES (
    'cable-row',
    'cable-machine',
    '坐姿绳索划船',
    '',
    '["背阔肌", "菱形肌", "肱二头肌"]'::jsonb,
    '["挺胸保持脊柱中立", "肘部向后拉"]'::jsonb,
    '["身体大幅后仰", "耸肩"]'::jsonb,
    'published'
);

COMMIT;

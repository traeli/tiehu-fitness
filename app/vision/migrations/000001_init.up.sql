BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE media_assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    media_type VARCHAR(24) NOT NULL,
    uri TEXT NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    duration_seconds NUMERIC(10,3) NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_media_assets_type CHECK (media_type IN ('image', 'video')),
    CONSTRAINT ck_media_assets_status CHECK (status IN ('pending', 'ready', 'expired', 'failed')),
    CONSTRAINT ck_media_assets_size CHECK (size_bytes >= 0),
    CONSTRAINT ck_media_assets_duration CHECK (duration_seconds >= 0)
);

CREATE INDEX idx_media_assets_user_id ON media_assets (user_id);
CREATE INDEX idx_media_assets_type ON media_assets (media_type);
CREATE INDEX idx_media_assets_status ON media_assets (status);

CREATE TABLE model_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_type VARCHAR(48) NOT NULL,
    version VARCHAR(64) NOT NULL,
    artifact_uri TEXT NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'inactive',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    activated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_model_type_version UNIQUE (model_type, version),
    CONSTRAINT ck_model_versions_status CHECK (status IN ('inactive', 'active', 'retired'))
);

CREATE INDEX idx_model_versions_status ON model_versions (status);

CREATE TABLE analysis_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    media_asset_id UUID NOT NULL REFERENCES media_assets (id) ON DELETE RESTRICT,
    analysis_type VARCHAR(24) NOT NULL,
    exercise_code VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    model_version_id UUID REFERENCES model_versions (id) ON DELETE SET NULL,
    attempt_count SMALLINT NOT NULL DEFAULT 0,
    result_json JSONB,
    error_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_analysis_jobs_type CHECK (analysis_type IN ('equipment', 'posture')),
    CONSTRAINT ck_analysis_jobs_status CHECK (status IN ('pending', 'processing', 'succeeded', 'failed')),
    CONSTRAINT ck_analysis_jobs_attempts CHECK (attempt_count >= 0)
);

CREATE INDEX idx_analysis_jobs_user_id ON analysis_jobs (user_id);
CREATE INDEX idx_analysis_jobs_media_asset_id ON analysis_jobs (media_asset_id);
CREATE INDEX idx_analysis_jobs_type ON analysis_jobs (analysis_type);
CREATE INDEX idx_analysis_jobs_status_created_at ON analysis_jobs (status, created_at);
CREATE INDEX idx_analysis_jobs_exercise_code
    ON analysis_jobs (exercise_code)
    WHERE exercise_code <> '';
CREATE INDEX idx_analysis_jobs_model_version_id ON analysis_jobs (model_version_id);

CREATE TABLE equipment_recognition_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL UNIQUE REFERENCES analysis_jobs (id) ON DELETE CASCADE,
    equipment_code VARCHAR(64) NOT NULL,
    confidence NUMERIC(6,5) NOT NULL,
    candidates JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_equipment_recognition_confidence CHECK (confidence BETWEEN 0 AND 1)
);

CREATE INDEX idx_equipment_recognition_code
    ON equipment_recognition_results (equipment_code);

CREATE TABLE posture_analysis_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL UNIQUE REFERENCES analysis_jobs (id) ON DELETE CASCADE,
    exercise_code VARCHAR(64) NOT NULL,
    score NUMERIC(5,2) NOT NULL DEFAULT 0,
    rep_count INTEGER NOT NULL DEFAULT 0,
    summary TEXT NOT NULL DEFAULT '',
    issues JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_posture_analysis_score CHECK (score BETWEEN 0 AND 100),
    CONSTRAINT ck_posture_analysis_reps CHECK (rep_count >= 0)
);

CREATE INDEX idx_posture_analysis_exercise_code
    ON posture_analysis_results (exercise_code);

COMMIT;

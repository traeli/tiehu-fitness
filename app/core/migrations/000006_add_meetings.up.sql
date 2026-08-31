BEGIN;

CREATE TABLE meetings (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reservation_id UUID NOT NULL UNIQUE,
    create_idempotency_key VARCHAR(64) NOT NULL,
    create_request_fingerprint CHAR(64) NOT NULL,
    stop_idempotency_key VARCHAR(64),
    status VARCHAR(32) NOT NULL,
    transcription_status VARCHAR(24) NOT NULL,
    language VARCHAR(16) NOT NULL,
    retain_audio BOOLEAN NOT NULL DEFAULT FALSE,
    granted_audio_seconds BIGINT NOT NULL,
    transcription_session_id UUID,
    websocket_url TEXT NOT NULL DEFAULT '',
    session_expires_at TIMESTAMPTZ,
    audio_mime_type VARCHAR(64) NOT NULL DEFAULT '',
    audio_sample_rate INTEGER NOT NULL DEFAULT 0,
    audio_channels SMALLINT NOT NULL DEFAULT 0,
    audio_chunk_duration_ms INTEGER NOT NULL DEFAULT 0,
    audio_max_chunk_bytes INTEGER NOT NULL DEFAULT 0,
    transcript_revision BIGINT NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL,
    stopped_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT fk_meeting_usage_reservation FOREIGN KEY (reservation_id)
        REFERENCES meeting_usage_reservations(reservation_id)
        DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT uk_meeting_user_create_key UNIQUE (user_id, create_idempotency_key),
    CONSTRAINT chk_meeting_create_fingerprint CHECK (create_request_fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chk_meeting_status CHECK (status IN ('recording', 'processing', 'completed', 'partially_completed', 'failed', 'cancelled')),
    CONSTRAINT chk_meeting_transcription_status CHECK (transcription_status IN ('pending', 'connecting', 'streaming', 'finishing', 'succeeded', 'failed', 'cancelled', 'expired')),
    CONSTRAINT chk_meeting_language CHECK (language IN ('auto', 'zh_cn', 'en_us')),
    CONSTRAINT chk_meeting_grant CHECK (granted_audio_seconds > 0),
    CONSTRAINT chk_meeting_transcript_revision CHECK (transcript_revision >= 0),
    CONSTRAINT chk_meeting_stop_time CHECK (stopped_at IS NULL OR stopped_at >= started_at),
    CONSTRAINT chk_meeting_session_fields CHECK (
        (transcription_session_id IS NULL AND websocket_url = '' AND session_expires_at IS NULL AND audio_mime_type = '' AND audio_sample_rate = 0 AND audio_channels = 0 AND audio_chunk_duration_ms = 0 AND audio_max_chunk_bytes = 0)
        OR
        (transcription_session_id IS NOT NULL AND websocket_url <> '' AND session_expires_at IS NOT NULL AND audio_mime_type <> '' AND audio_sample_rate > 0 AND audio_channels > 0 AND audio_chunk_duration_ms > 0 AND audio_max_chunk_bytes > 0)
    )
);

CREATE UNIQUE INDEX uk_meeting_user_stop_key
    ON meetings(user_id, stop_idempotency_key)
    WHERE stop_idempotency_key IS NOT NULL;

CREATE UNIQUE INDEX uk_meeting_transcription_session
    ON meetings(transcription_session_id)
    WHERE transcription_session_id IS NOT NULL;

CREATE INDEX idx_meeting_user_created
    ON meetings(user_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_meeting_active
    ON meetings(user_id, status, started_at)
    WHERE deleted_at IS NULL AND status = 'recording';

CREATE TABLE meeting_transcript_segments (
    id UUID PRIMARY KEY,
    meeting_id UUID NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
    segment_id UUID NOT NULL,
    sequence_no BIGINT NOT NULL,
    start_offset_ms BIGINT NOT NULL,
    end_offset_ms BIGINT NOT NULL,
    speaker_label VARCHAR(80) NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    language VARCHAR(16) NOT NULL,
    confidence REAL,
    created_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_meeting_segment_sequence UNIQUE (meeting_id, sequence_no),
    CONSTRAINT uk_meeting_segment_id UNIQUE (meeting_id, segment_id),
    CONSTRAINT chk_meeting_segment_sequence CHECK (sequence_no > 0),
    CONSTRAINT chk_meeting_segment_offsets CHECK (start_offset_ms >= 0 AND end_offset_ms >= start_offset_ms),
    CONSTRAINT chk_meeting_segment_content CHECK (char_length(content) BETWEEN 1 AND 20000),
    CONSTRAINT chk_meeting_segment_language CHECK (language IN ('zh_cn', 'en_us')),
    CONSTRAINT chk_meeting_segment_confidence CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1))
);

CREATE INDEX idx_meeting_segment_page
    ON meeting_transcript_segments(meeting_id, sequence_no ASC);

CREATE TABLE meeting_transcript_batches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    meeting_id UUID NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
    batch_id UUID NOT NULL,
    last_sequence_no BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_meeting_transcript_batch UNIQUE (meeting_id, batch_id),
    CONSTRAINT chk_meeting_transcript_batch_sequence CHECK (last_sequence_no > 0)
);

COMMIT;

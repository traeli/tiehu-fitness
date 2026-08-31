package model

import (
	"encoding/json"
	"time"
)

type MediaAsset struct {
	ID              string          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID          string          `gorm:"column:user_id;type:uuid;not null;index"`
	MediaType       string          `gorm:"column:media_type;size:24;not null;index"`
	URI             string          `gorm:"column:uri;type:text;not null"`
	Status          string          `gorm:"size:24;not null;default:pending;index"`
	SizeBytes       int64           `gorm:"column:size_bytes;not null;default:0"`
	DurationSeconds float64         `gorm:"column:duration_seconds;type:numeric(10,3);not null;default:0"`
	Metadata        json.RawMessage `gorm:"type:jsonb;not null"`
	CreatedAt       time.Time       `gorm:"not null;autoCreateTime"`
	UpdatedAt       time.Time       `gorm:"not null;autoUpdateTime"`
}

func (MediaAsset) TableName() string { return "media_assets" }

type ModelVersion struct {
	ID          string          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ModelType   string          `gorm:"column:model_type;size:48;not null;uniqueIndex:uk_model_type_version,priority:1"`
	Version     string          `gorm:"size:64;not null;uniqueIndex:uk_model_type_version,priority:2"`
	ArtifactURI string          `gorm:"column:artifact_uri;type:text;not null"`
	Status      string          `gorm:"size:24;not null;default:inactive;index"`
	Metadata    json.RawMessage `gorm:"type:jsonb;not null"`
	ActivatedAt *time.Time      `gorm:"column:activated_at"`
	CreatedAt   time.Time       `gorm:"not null;autoCreateTime"`
	UpdatedAt   time.Time       `gorm:"not null;autoUpdateTime"`
}

func (ModelVersion) TableName() string { return "model_versions" }

type AnalysisJob struct {
	ID             string          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID         string          `gorm:"column:user_id;type:uuid;not null;index"`
	MediaAssetID   string          `gorm:"column:media_asset_id;type:uuid;not null;index"`
	AnalysisType   string          `gorm:"column:analysis_type;size:24;not null;index"`
	ExerciseCode   string          `gorm:"column:exercise_code;size:64;not null;default:'';index"`
	Status         string          `gorm:"size:24;not null;default:pending;index"`
	ModelVersionID *string         `gorm:"column:model_version_id;type:uuid;index"`
	AttemptCount   int16           `gorm:"column:attempt_count;not null;default:0"`
	ResultJSON     json.RawMessage `gorm:"column:result_json;type:jsonb"`
	ErrorMessage   string          `gorm:"column:error_message;type:text;not null;default:''"`
	StartedAt      *time.Time      `gorm:"column:started_at"`
	FinishedAt     *time.Time      `gorm:"column:finished_at"`
	CreatedAt      time.Time       `gorm:"not null;autoCreateTime;index"`
	UpdatedAt      time.Time       `gorm:"not null;autoUpdateTime"`
}

func (AnalysisJob) TableName() string { return "analysis_jobs" }

type EquipmentRecognitionResult struct {
	ID            string          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	JobID         string          `gorm:"column:job_id;type:uuid;not null;unique"`
	EquipmentCode string          `gorm:"column:equipment_code;size:64;not null;index"`
	Confidence    float64         `gorm:"type:numeric(6,5);not null"`
	Candidates    json.RawMessage `gorm:"type:jsonb;not null"`
	CreatedAt     time.Time       `gorm:"not null;autoCreateTime"`
}

func (EquipmentRecognitionResult) TableName() string {
	return "equipment_recognition_results"
}

type PostureAnalysisResult struct {
	ID           string          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	JobID        string          `gorm:"column:job_id;type:uuid;not null;unique"`
	ExerciseCode string          `gorm:"column:exercise_code;size:64;not null;index"`
	Score        float64         `gorm:"type:numeric(5,2);not null;default:0"`
	RepCount     int32           `gorm:"column:rep_count;not null;default:0"`
	Summary      string          `gorm:"type:text;not null;default:''"`
	Issues       json.RawMessage `gorm:"type:jsonb;not null"`
	CreatedAt    time.Time       `gorm:"not null;autoCreateTime"`
}

func (PostureAnalysisResult) TableName() string { return "posture_analysis_results" }

type TranscriptionSession struct {
	ID                       string     `gorm:"type:uuid;primaryKey"`
	ProviderConfigID         string     `gorm:"column:provider_config_id;type:uuid;not null;index"`
	MeetingID                string     `gorm:"column:meeting_id;type:uuid;not null;unique"`
	UserID                   string     `gorm:"column:user_id;type:uuid;not null;index"`
	ReservationID            string     `gorm:"column:reservation_id;type:uuid;not null"`
	Language                 string     `gorm:"size:16;not null"`
	Status                   string     `gorm:"size:24;not null;index"`
	Provider                 string     `gorm:"size:32;not null"`
	IdempotencyKey           string     `gorm:"column:idempotency_key;size:128;not null;unique"`
	GrantedAudioMilliseconds int64      `gorm:"column:granted_audio_milliseconds;not null"`
	AcceptedAudioBytes       int64      `gorm:"column:accepted_audio_bytes;not null"`
	LastAudioSequence        int64      `gorm:"column:last_audio_sequence;not null"`
	FailureCode              string     `gorm:"column:failure_code;size:64;not null"`
	StartedAt                *time.Time `gorm:"column:started_at"`
	FinishedAt               *time.Time `gorm:"column:finished_at"`
	CreatedAt                time.Time  `gorm:"not null"`
	UpdatedAt                time.Time  `gorm:"not null"`
}

func (TranscriptionSession) TableName() string { return "transcription_sessions" }

type TranscriptionAudioChunk struct {
	SessionID  string    `gorm:"column:session_id;type:uuid;primaryKey"`
	SequenceNo int64     `gorm:"column:sequence_no;primaryKey"`
	SizeBytes  int32     `gorm:"column:size_bytes;not null"`
	CreatedAt  time.Time `gorm:"not null"`
}

func (TranscriptionAudioChunk) TableName() string { return "transcription_audio_chunks" }

type ASRJob struct {
	ID           string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	SessionID    string    `gorm:"column:session_id;type:uuid;not null;unique"`
	Status       string    `gorm:"size:24;not null"`
	Provider     string    `gorm:"size:32;not null"`
	AttemptCount int32     `gorm:"column:attempt_count;not null"`
	CreatedAt    time.Time `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
}

func (ASRJob) TableName() string { return "asr_jobs" }

type AIJobAttempt struct {
	ID            string     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	JobID         string     `gorm:"column:job_id;type:uuid;not null;uniqueIndex:uk_ai_job_attempts_number,priority:1"`
	AttemptNumber int32      `gorm:"column:attempt_number;not null;uniqueIndex:uk_ai_job_attempts_number,priority:2"`
	Provider      string     `gorm:"size:32;not null"`
	Status        string     `gorm:"size:24;not null"`
	ErrorCode     string     `gorm:"column:error_code;size:64;not null"`
	StartedAt     time.Time  `gorm:"column:started_at;not null"`
	FinishedAt    *time.Time `gorm:"column:finished_at"`
}

func (AIJobAttempt) TableName() string { return "ai_job_attempts" }

type TranscriptionFinalSegment struct {
	ID                string    `gorm:"type:uuid;primaryKey"`
	SessionID         string    `gorm:"column:session_id;type:uuid;not null;uniqueIndex:uk_transcription_final_segments_sequence,priority:1"`
	SequenceNo        int64     `gorm:"column:sequence_no;not null;uniqueIndex:uk_transcription_final_segments_sequence,priority:2"`
	StartMilliseconds int64     `gorm:"column:start_milliseconds;not null"`
	EndMilliseconds   int64     `gorm:"column:end_milliseconds;not null"`
	SpeakerLabel      string    `gorm:"column:speaker_label;size:64;not null"`
	Content           string    `gorm:"type:text;not null"`
	Language          string    `gorm:"size:16;not null"`
	Confidence        float64   `gorm:"not null"`
	CreatedAt         time.Time `gorm:"not null"`
}

func (TranscriptionFinalSegment) TableName() string { return "transcription_final_segments" }

type TranscriptionOutbox struct {
	ID           string          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	SessionID    string          `gorm:"column:session_id;type:uuid;not null;index"`
	EventType    string          `gorm:"column:event_type;size:48;not null"`
	Payload      json.RawMessage `gorm:"type:jsonb;not null"`
	Status       string          `gorm:"size:16;not null"`
	AttemptCount int32           `gorm:"column:attempt_count;not null"`
	AvailableAt  time.Time       `gorm:"column:available_at;not null"`
	DeliveredAt  *time.Time      `gorm:"column:delivered_at"`
	CreatedAt    time.Time       `gorm:"not null"`
	UpdatedAt    time.Time       `gorm:"not null"`
}

func (TranscriptionOutbox) TableName() string { return "transcription_outbox" }

type MeetingSummaryJob struct {
	ID                       string          `gorm:"type:uuid;primaryKey"`
	ProviderConfigID         string          `gorm:"column:provider_config_id;type:uuid;not null;index"`
	MeetingID                string          `gorm:"column:meeting_id;type:uuid;not null;uniqueIndex:uk_meeting_summary_job_version,priority:1;uniqueIndex:uk_meeting_summary_job_idempotency,priority:1"`
	UserID                   string          `gorm:"column:user_id;type:uuid;not null;index"`
	Version                  int64           `gorm:"not null;uniqueIndex:uk_meeting_summary_job_version,priority:2"`
	SourceTranscriptRevision int64           `gorm:"column:source_transcript_revision;not null"`
	Language                 string          `gorm:"size:16;not null"`
	IdempotencyKey           string          `gorm:"column:idempotency_key;size:128;not null;uniqueIndex:uk_meeting_summary_job_idempotency,priority:2"`
	Status                   string          `gorm:"size:24;not null;index"`
	Provider                 string          `gorm:"size:32;not null"`
	ModelName                string          `gorm:"column:model_name;size:128;not null"`
	PromptVersion            string          `gorm:"column:prompt_version;size:64;not null"`
	ResultJSON               json.RawMessage `gorm:"column:result_json;type:jsonb"`
	LLMRequest               string          `gorm:"column:llm_request;type:text;not null;default:''"`
	LLMResponse              string          `gorm:"column:llm_response;type:text;not null;default:''"`
	LLMHTTPStatus            int32           `gorm:"column:llm_http_status;not null;default:0"`
	LLMDurationMilliseconds  int64           `gorm:"column:llm_duration_milliseconds;not null;default:0"`
	LLMFailure               string          `gorm:"column:llm_failure;type:text;not null;default:''"`
	InputTokens              int64           `gorm:"column:input_tokens;not null;default:0"`
	OutputTokens             int64           `gorm:"column:output_tokens;not null;default:0"`
	FailureReason            string          `gorm:"column:failure_reason;size:64;not null;default:''"`
	AttemptCount             int32           `gorm:"column:attempt_count;not null;default:0"`
	AvailableAt              time.Time       `gorm:"column:available_at;not null;index"`
	StartedAt                *time.Time      `gorm:"column:started_at"`
	FinishedAt               *time.Time      `gorm:"column:finished_at"`
	CreatedAt                time.Time       `gorm:"not null"`
	UpdatedAt                time.Time       `gorm:"not null"`
}

func (MeetingSummaryJob) TableName() string { return "meeting_summary_jobs" }

type ASRProviderConfig struct {
	ID            string     `gorm:"type:uuid;primaryKey"`
	Version       int64      `gorm:"not null;unique"`
	Status        string     `gorm:"size:16;not null;index"`
	Provider      string     `gorm:"size:32;not null"`
	WorkspaceID   string     `gorm:"column:workspace_id;size:63;not null"`
	RealtimeModel string     `gorm:"column:realtime_model;size:128;not null"`
	FileModel     string     `gorm:"column:file_model;size:128;not null"`
	VocabularyID  string     `gorm:"column:vocabulary_id;size:128;not null"`
	ActivatedAt   *time.Time `gorm:"column:activated_at"`
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
}

func (ASRProviderConfig) TableName() string { return "asr_provider_configs" }

type MeetingSummaryProviderConfig struct {
	ID                    string     `gorm:"type:uuid;primaryKey"`
	Version               int64      `gorm:"not null;unique"`
	Status                string     `gorm:"size:16;not null;index"`
	Provider              string     `gorm:"size:32;not null"`
	ModelName             string     `gorm:"column:model_name;size:128;not null"`
	PromptVersion         string     `gorm:"column:prompt_version;size:64;not null"`
	MaxInputCharsPerChunk int32      `gorm:"column:max_input_chars_per_chunk;not null"`
	MaxChunks             int32      `gorm:"column:max_chunks;not null"`
	MaxOutputTokens       int32      `gorm:"column:max_output_tokens;not null"`
	ActivatedAt           *time.Time `gorm:"column:activated_at"`
	CreatedAt             time.Time  `gorm:"not null"`
	UpdatedAt             time.Time  `gorm:"not null"`
}

func (MeetingSummaryProviderConfig) TableName() string {
	return "meeting_summary_provider_configs"
}

type ProviderCredential struct {
	Provider  string    `gorm:"primaryKey;size:32"`
	APIKey    string    `gorm:"column:api_key;type:text;not null"`
	Version   int64     `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (ProviderCredential) TableName() string { return "provider_credentials" }

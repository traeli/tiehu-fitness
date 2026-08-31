package model

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID        string         `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Nickname  string         `gorm:"size:80;not null;default:''"`
	AvatarURI string         `gorm:"column:avatar_uri;type:text;not null;default:''"`
	Status    string         `gorm:"size:24;not null;default:active;index"`
	CreatedAt time.Time      `gorm:"not null;autoCreateTime"`
	UpdatedAt time.Time      `gorm:"not null;autoUpdateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (User) TableName() string { return "users" }

type WechatIdentity struct {
	ID                   string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID               string    `gorm:"type:uuid;not null;index"`
	AppID                string    `gorm:"column:app_id;size:64;not null;uniqueIndex:uk_wechat_app_openid,priority:1"`
	OpenID               string    `gorm:"column:open_id;size:128;not null;uniqueIndex:uk_wechat_app_openid,priority:2"`
	UnionID              string    `gorm:"column:union_id;size:128;index"`
	SessionKeyCiphertext []byte    `gorm:"column:session_key_ciphertext"`
	CreatedAt            time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt            time.Time `gorm:"not null;autoUpdateTime"`
}

func (WechatIdentity) TableName() string { return "wechat_identities" }

type UToolsIdentity struct {
	ID        string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID    string    `gorm:"column:user_id;type:uuid;not null;index"`
	PluginID  string    `gorm:"column:plugin_id;size:64;not null;uniqueIndex:uk_utools_plugin_openid,priority:1"`
	OpenID    string    `gorm:"column:open_id;size:128;not null;uniqueIndex:uk_utools_plugin_openid,priority:2"`
	Member    bool      `gorm:"not null;default:false"`
	CreatedAt time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime"`
}

func (UToolsIdentity) TableName() string { return "utools_identities" }

type PasswordCredential struct {
	ID           string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID       string    `gorm:"column:user_id;type:uuid;not null;unique"`
	Email        string    `gorm:"size:254;not null;unique"`
	PasswordHash string    `gorm:"column:password_hash;type:text;not null"`
	CreatedAt    time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt    time.Time `gorm:"not null;autoUpdateTime"`
}

func (PasswordCredential) TableName() string { return "password_credentials" }

type UserSession struct {
	ID               string     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID           string     `gorm:"type:uuid;not null;index"`
	DeviceID         string     `gorm:"column:device_id;size:128;not null;default:''"`
	AccessTokenHash  string     `gorm:"column:access_token_hash;size:128;not null;unique"`
	RefreshTokenHash string     `gorm:"column:refresh_token_hash;size:128;not null;unique"`
	AccessExpiresAt  time.Time  `gorm:"column:access_expires_at;not null;index"`
	RefreshExpiresAt time.Time  `gorm:"column:refresh_expires_at;not null;index"`
	RevokedAt        *time.Time `gorm:"column:revoked_at;index"`
	CreatedAt        time.Time  `gorm:"not null;autoCreateTime"`
	UpdatedAt        time.Time  `gorm:"not null;autoUpdateTime"`
}

func (UserSession) TableName() string { return "user_sessions" }

type FitnessProfile struct {
	UserID                  string          `gorm:"column:user_id;type:uuid;primaryKey"`
	Goal                    string          `gorm:"size:32;not null;default:''"`
	ExperienceLevel         string          `gorm:"column:experience_level;size:32;not null;default:''"`
	DaysPerWeek             int16           `gorm:"column:days_per_week;not null;default:0"`
	DurationMinutes         int16           `gorm:"column:duration_minutes;not null;default:0"`
	AvailableEquipmentCodes json.RawMessage `gorm:"column:available_equipment_codes;type:jsonb;not null"`
	InjuryNotes             json.RawMessage `gorm:"column:injury_notes;type:jsonb;not null"`
	OnboardingCompleted     bool            `gorm:"column:onboarding_completed;not null;default:false"`
	CreatedAt               time.Time       `gorm:"not null;autoCreateTime"`
	UpdatedAt               time.Time       `gorm:"not null;autoUpdateTime"`
}

func (FitnessProfile) TableName() string { return "fitness_profiles" }

type Equipment struct {
	Code          string          `gorm:"size:64;primaryKey"`
	Name          string          `gorm:"size:120;not null"`
	Description   string          `gorm:"type:text;not null;default:''"`
	TargetMuscles json.RawMessage `gorm:"column:target_muscles;type:jsonb;not null"`
	SafetyTips    json.RawMessage `gorm:"column:safety_tips;type:jsonb;not null"`
	Status        string          `gorm:"size:24;not null;default:draft;index"`
	CreatedAt     time.Time       `gorm:"not null;autoCreateTime"`
	UpdatedAt     time.Time       `gorm:"not null;autoUpdateTime"`
}

func (Equipment) TableName() string { return "equipment" }

type Exercise struct {
	Code                string          `gorm:"size:64;primaryKey"`
	EquipmentCode       string          `gorm:"column:equipment_code;size:64;not null;index"`
	Name                string          `gorm:"size:120;not null"`
	InstructionVideoURI string          `gorm:"column:instruction_video_uri;type:text;not null;default:''"`
	TargetMuscles       json.RawMessage `gorm:"column:target_muscles;type:jsonb;not null"`
	KeyPoints           json.RawMessage `gorm:"column:key_points;type:jsonb;not null"`
	CommonMistakes      json.RawMessage `gorm:"column:common_mistakes;type:jsonb;not null"`
	Status              string          `gorm:"size:24;not null;default:draft;index"`
	CreatedAt           time.Time       `gorm:"not null;autoCreateTime"`
	UpdatedAt           time.Time       `gorm:"not null;autoUpdateTime"`
}

func (Exercise) TableName() string { return "exercises" }

type TrainingPlan struct {
	ID        string     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID    string     `gorm:"column:user_id;type:uuid;not null;index"`
	Goal      string     `gorm:"size:32;not null"`
	Status    string     `gorm:"size:24;not null;default:draft;index"`
	StartsOn  *time.Time `gorm:"column:starts_on;type:date"`
	CreatedAt time.Time  `gorm:"not null;autoCreateTime"`
	UpdatedAt time.Time  `gorm:"not null;autoUpdateTime"`
}

func (TrainingPlan) TableName() string { return "training_plans" }

type TrainingPlanItem struct {
	ID           string  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	PlanID       string  `gorm:"column:plan_id;type:uuid;not null;uniqueIndex:uk_plan_day_order,priority:1"`
	DayNumber    int16   `gorm:"column:day_number;not null;uniqueIndex:uk_plan_day_order,priority:2"`
	ExerciseCode string  `gorm:"column:exercise_code;size:64;not null;index"`
	Sets         int16   `gorm:"not null;default:0"`
	Reps         int16   `gorm:"not null;default:0"`
	WeightKG     float64 `gorm:"column:weight_kg;type:numeric(8,2);not null;default:0"`
	SortOrder    int16   `gorm:"column:sort_order;not null;uniqueIndex:uk_plan_day_order,priority:3"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (TrainingPlanItem) TableName() string { return "training_plan_items" }

type WorkoutSession struct {
	ID              string     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID          string     `gorm:"column:user_id;type:uuid;not null;index"`
	PlanID          *string    `gorm:"column:plan_id;type:uuid;index"`
	Status          string     `gorm:"size:24;not null;default:in_progress;index"`
	StartedAt       time.Time  `gorm:"column:started_at;not null;index"`
	CompletedAt     *time.Time `gorm:"column:completed_at"`
	DurationSeconds int32      `gorm:"column:duration_seconds;not null;default:0"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (WorkoutSession) TableName() string { return "workout_sessions" }

type WorkoutSet struct {
	ID           string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	SessionID    string    `gorm:"column:session_id;type:uuid;not null;uniqueIndex:uk_session_exercise_set,priority:1"`
	ExerciseCode string    `gorm:"column:exercise_code;size:64;not null;uniqueIndex:uk_session_exercise_set,priority:2"`
	SetNumber    int16     `gorm:"column:set_number;not null;uniqueIndex:uk_session_exercise_set,priority:3"`
	Reps         int16     `gorm:"not null;default:0"`
	WeightKG     float64   `gorm:"column:weight_kg;type:numeric(8,2);not null;default:0"`
	RPE          *float64  `gorm:"column:rpe;type:numeric(3,1)"`
	RestSeconds  int32     `gorm:"column:rest_seconds;not null;default:0"`
	CompletedAt  time.Time `gorm:"column:completed_at;not null"`
	CreatedAt    time.Time
}

func (WorkoutSet) TableName() string { return "workout_sets" }

type CheckIn struct {
	ID        string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID    string    `gorm:"column:user_id;type:uuid;not null;uniqueIndex:uk_user_checkin_date,priority:1"`
	CheckDate time.Time `gorm:"column:check_date;type:date;not null;uniqueIndex:uk_user_checkin_date,priority:2"`
	CreatedAt time.Time `gorm:"not null;autoCreateTime"`
}

func (CheckIn) TableName() string { return "check_ins" }

type MeetingQuotaPolicy struct {
	ID                         int16     `gorm:"column:id;primaryKey"`
	MonthlyAudioSeconds        int64     `gorm:"column:monthly_audio_seconds;not null"`
	MaxMeetingAudioSeconds     int64     `gorm:"column:max_meeting_audio_seconds;not null"`
	MaxConcurrentMeetings      int32     `gorm:"column:max_concurrent_meetings;not null"`
	CreateRateLimit            int32     `gorm:"column:create_rate_limit;not null"`
	CreateRateWindowSeconds    int64     `gorm:"column:create_rate_window_seconds;not null"`
	PeriodTimezone             string    `gorm:"column:period_timezone;size:64;not null"`
	UsageReportIntervalSeconds int64     `gorm:"column:usage_report_interval_seconds;not null"`
	ReservationTTLSeconds      int64     `gorm:"column:reservation_ttl_seconds;not null"`
	RedisFailurePolicy         string    `gorm:"column:redis_failure_policy;size:32;not null"`
	Version                    int64     `gorm:"column:version;not null"`
	CreatedAt                  time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt                  time.Time `gorm:"not null;autoUpdateTime"`
}

func (MeetingQuotaPolicy) TableName() string { return "meeting_quota_policies" }

type UserMeetingQuotaOverride struct {
	UserID                 string    `gorm:"column:user_id;type:uuid;primaryKey"`
	Status                 string    `gorm:"size:16;not null;default:active"`
	MonthlyAudioSeconds    *int64    `gorm:"column:monthly_audio_seconds"`
	MaxMeetingAudioSeconds *int64    `gorm:"column:max_meeting_audio_seconds"`
	MaxConcurrentMeetings  *int32    `gorm:"column:max_concurrent_meetings"`
	CreatedAt              time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt              time.Time `gorm:"not null;autoUpdateTime"`
}

func (UserMeetingQuotaOverride) TableName() string { return "user_meeting_quota_overrides" }

type MeetingUsagePeriod struct {
	ID                    string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID                string    `gorm:"column:user_id;type:uuid;not null;uniqueIndex:uk_usage_period_user_start,priority:1"`
	PeriodStart           time.Time `gorm:"column:period_start;not null;uniqueIndex:uk_usage_period_user_start,priority:2"`
	PeriodEnd             time.Time `gorm:"column:period_end;not null"`
	BaseQuotaSeconds      int64     `gorm:"column:base_quota_seconds;not null;default:0"`
	PurchasedQuotaSeconds int64     `gorm:"column:purchased_quota_seconds;not null;default:0"`
	ConsumedSeconds       int64     `gorm:"column:consumed_seconds;not null;default:0"`
	ReservedSeconds       int64     `gorm:"column:reserved_seconds;not null;default:0"`
	CreatedAt             time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt             time.Time `gorm:"not null;autoUpdateTime"`
}

func (MeetingUsagePeriod) TableName() string { return "meeting_usage_periods" }

// Order stores the payment record that grants quota to one monthly period.
// Payment callbacks will use ExternalOrderID as their idempotency boundary.
type Order struct {
	ID               string     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID           string     `gorm:"column:user_id;type:uuid;not null;index"`
	UsagePeriodID    string     `gorm:"column:usage_period_id;type:uuid;not null;index"`
	Type             string     `gorm:"column:type;size:32;not null;index"`
	Status           string     `gorm:"column:status;size:16;not null;default:pending;index"`
	ExternalOrderID  *string    `gorm:"column:external_order_id;size:128;uniqueIndex"`
	PurchasedSeconds int64      `gorm:"column:purchased_seconds;not null"`
	PaidAt           *time.Time `gorm:"column:paid_at"`
	CreatedAt        time.Time  `gorm:"not null;autoCreateTime"`
	UpdatedAt        time.Time  `gorm:"not null;autoUpdateTime"`
}

func (Order) TableName() string { return "orders" }

type MeetingUsageReservation struct {
	ID              string     `gorm:"column:reservation_id;type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID          string     `gorm:"column:user_id;type:uuid;not null;index"`
	MeetingID       string     `gorm:"column:meeting_id;type:uuid;not null;unique"`
	PeriodStart     time.Time  `gorm:"column:period_start;not null;index"`
	PeriodEnd       time.Time  `gorm:"column:period_end;not null"`
	GrantedSeconds  int64      `gorm:"column:granted_seconds;not null"`
	ReportedSeconds int64      `gorm:"column:reported_seconds;not null;default:0"`
	Status          string     `gorm:"size:16;not null;default:active;index"`
	ExpiresAt       time.Time  `gorm:"column:expires_at;not null;index"`
	FinalizedAt     *time.Time `gorm:"column:finalized_at"`
	CreatedAt       time.Time  `gorm:"not null;autoCreateTime"`
	UpdatedAt       time.Time  `gorm:"not null;autoUpdateTime"`
}

func (MeetingUsageReservation) TableName() string { return "meeting_usage_reservations" }

type MeetingUsageRecord struct {
	ID                   string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ReservationID        string    `gorm:"column:reservation_id;type:uuid;not null;unique"`
	UserID               string    `gorm:"column:user_id;type:uuid;not null;index"`
	MeetingID            string    `gorm:"column:meeting_id;type:uuid;not null;uniqueIndex:uk_meeting_usage_kind,priority:1"`
	PeriodStart          time.Time `gorm:"column:period_start;not null;index"`
	PeriodEnd            time.Time `gorm:"column:period_end;not null"`
	UsageKind            string    `gorm:"column:usage_kind;size:24;not null;uniqueIndex:uk_meeting_usage_kind,priority:2"`
	ActualSeconds        int64     `gorm:"column:actual_seconds;not null"`
	ProviderUsageSeconds int64     `gorm:"column:provider_usage_seconds;not null;default:0"`
	SettlementReason     string    `gorm:"column:settlement_reason;size:32;not null"`
	SettledAt            time.Time `gorm:"column:settled_at;not null"`
	CreatedAt            time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt            time.Time `gorm:"not null;autoUpdateTime"`
}

func (MeetingUsageRecord) TableName() string { return "meeting_usage_records" }

type Meeting struct {
	ID                              string          `gorm:"type:uuid;primaryKey"`
	UserID                          string          `gorm:"column:user_id;type:uuid;not null;uniqueIndex:uk_meeting_user_create_key,priority:1"`
	ReservationID                   string          `gorm:"column:reservation_id;type:uuid;not null;unique"`
	CreateIdempotencyKey            string          `gorm:"column:create_idempotency_key;size:64;not null;uniqueIndex:uk_meeting_user_create_key,priority:2"`
	CreateRequestFingerprint        string          `gorm:"column:create_request_fingerprint;size:64;not null"`
	StopIdempotencyKey              *string         `gorm:"column:stop_idempotency_key;size:64"`
	Status                          string          `gorm:"size:32;not null;index"`
	TranscriptionStatus             string          `gorm:"column:transcription_status;size:24;not null;index"`
	Language                        string          `gorm:"size:16;not null"`
	RetainAudio                     bool            `gorm:"column:retain_audio;not null;default:false"`
	GrantedAudioSeconds             int64           `gorm:"column:granted_audio_seconds;not null"`
	TranscriptionSessionID          *string         `gorm:"column:transcription_session_id;type:uuid"`
	WebSocketURL                    string          `gorm:"column:websocket_url;type:text;not null;default:''"`
	SessionExpiresAt                *time.Time      `gorm:"column:session_expires_at"`
	AudioMIMEType                   string          `gorm:"column:audio_mime_type;size:64;not null;default:''"`
	AudioSampleRate                 int32           `gorm:"column:audio_sample_rate;not null;default:0"`
	AudioChannels                   int16           `gorm:"column:audio_channels;not null;default:0"`
	AudioChunkDurationMS            int32           `gorm:"column:audio_chunk_duration_ms;not null;default:0"`
	AudioMaxChunkBytes              int32           `gorm:"column:audio_max_chunk_bytes;not null;default:0"`
	TranscriptRevision              int64           `gorm:"column:transcript_revision;not null;default:0"`
	SummaryStatus                   string          `gorm:"column:summary_status;size:24;not null;default:not_started;index"`
	SummaryVersion                  int64           `gorm:"column:summary_version;not null;default:0"`
	SummarySourceTranscriptRevision int64           `gorm:"column:summary_source_transcript_revision;not null;default:0"`
	SummaryIdempotencyKey           string          `gorm:"column:summary_idempotency_key;size:64;not null;default:''"`
	SummaryContent                  json.RawMessage `gorm:"column:summary_content;type:jsonb;not null;default:'{}'"`
	SummaryProvider                 string          `gorm:"column:summary_provider;size:32;not null;default:''"`
	SummaryModelName                string          `gorm:"column:summary_model_name;size:128;not null;default:''"`
	SummaryPromptVersion            string          `gorm:"column:summary_prompt_version;size:64;not null;default:''"`
	SummaryInputTokens              int64           `gorm:"column:summary_input_tokens;not null;default:0"`
	SummaryOutputTokens             int64           `gorm:"column:summary_output_tokens;not null;default:0"`
	SummaryFailureReason            string          `gorm:"column:summary_failure_reason;size:64;not null;default:''"`
	SummaryGeneratedAt              *time.Time      `gorm:"column:summary_generated_at"`
	StartedAt                       time.Time       `gorm:"column:started_at;not null;index"`
	StoppedAt                       *time.Time      `gorm:"column:stopped_at"`
	CreatedAt                       time.Time       `gorm:"not null;autoCreateTime;index"`
	UpdatedAt                       time.Time       `gorm:"not null;autoUpdateTime"`
	DeletedAt                       gorm.DeletedAt  `gorm:"index"`
}

func (Meeting) TableName() string { return "meetings" }

type MeetingTranscriptSegment struct {
	ID            string    `gorm:"type:uuid;primaryKey"`
	MeetingID     string    `gorm:"column:meeting_id;type:uuid;not null;uniqueIndex:uk_meeting_segment_sequence,priority:1;uniqueIndex:uk_meeting_segment_id,priority:1"`
	SegmentID     string    `gorm:"column:segment_id;type:uuid;not null;uniqueIndex:uk_meeting_segment_id,priority:2"`
	SequenceNo    int64     `gorm:"column:sequence_no;not null;uniqueIndex:uk_meeting_segment_sequence,priority:2"`
	StartOffsetMS int64     `gorm:"column:start_offset_ms;not null"`
	EndOffsetMS   int64     `gorm:"column:end_offset_ms;not null"`
	SpeakerLabel  string    `gorm:"column:speaker_label;size:80;not null;default:''"`
	Content       string    `gorm:"type:text;not null"`
	Language      string    `gorm:"size:16;not null"`
	Confidence    *float32  `gorm:"type:real"`
	CreatedAt     time.Time `gorm:"column:created_at;not null"`
	InsertedAt    time.Time `gorm:"column:inserted_at;not null;autoCreateTime"`
}

func (MeetingTranscriptSegment) TableName() string { return "meeting_transcript_segments" }

type MeetingTranscriptBatch struct {
	ID             string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	MeetingID      string    `gorm:"column:meeting_id;type:uuid;not null;uniqueIndex:uk_meeting_transcript_batch,priority:1"`
	BatchID        string    `gorm:"column:batch_id;type:uuid;not null;uniqueIndex:uk_meeting_transcript_batch,priority:2"`
	LastSequenceNo int64     `gorm:"column:last_sequence_no;not null"`
	CreatedAt      time.Time `gorm:"not null;autoCreateTime"`
}

func (MeetingTranscriptBatch) TableName() string { return "meeting_transcript_batches" }

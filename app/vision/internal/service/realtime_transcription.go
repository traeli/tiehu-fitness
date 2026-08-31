package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"strings"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

const (
	realtimeProtocolVersion  = int32(1)
	maxAudioHeaderBytes      = 4 * 1024
	pcmMIMEType              = "audio/pcm;rate=16000"
	maxRealtimeAudioSequence = int64((24 * time.Hour) / (100 * time.Millisecond))
)

type RealtimeClientMessageType string

const (
	RealtimeClientMessageTypeStart      RealtimeClientMessageType = "start"
	RealtimeClientMessageTypeAudioChunk RealtimeClientMessageType = "audio_chunk"
	RealtimeClientMessageTypePing       RealtimeClientMessageType = "ping"
	RealtimeClientMessageTypeFinish     RealtimeClientMessageType = "finish"
)

type RealtimeFinishReason string

const (
	RealtimeFinishReasonClientFinished RealtimeFinishReason = "client_finished"
	RealtimeFinishReasonQuotaExhausted RealtimeFinishReason = "quota_exhausted"
	RealtimeFinishReasonCancelled      RealtimeFinishReason = "cancelled"
	RealtimeFinishReasonExpired        RealtimeFinishReason = "expired"
)

type RealtimeServerMessageType string

const (
	RealtimeServerMessageTypeSessionReady      RealtimeServerMessageType = "session_ready"
	RealtimeServerMessageTypeACK               RealtimeServerMessageType = "ack"
	RealtimeServerMessageTypePong              RealtimeServerMessageType = "pong"
	RealtimeServerMessageTypeTranscriptSegment RealtimeServerMessageType = "transcript_segment"
	RealtimeServerMessageTypeSessionFinished   RealtimeServerMessageType = "session_finished"
	RealtimeServerMessageTypeError             RealtimeServerMessageType = "error"
)

type RealtimeAudioFormat string

const RealtimeAudioFormatPCM RealtimeAudioFormat = "pcm"

type RealtimeLanguage string

const (
	RealtimeLanguageAuto RealtimeLanguage = "auto"
	RealtimeLanguageZh   RealtimeLanguage = "zh"
	RealtimeLanguageEn   RealtimeLanguage = "en"
)

type RealtimeErrorCode string

const (
	RealtimeErrorCodeTicketInvalid       RealtimeErrorCode = "TRANSCRIPTION_TICKET_INVALID"
	RealtimeErrorCodeSessionExpired      RealtimeErrorCode = "TRANSCRIPTION_SESSION_EXPIRED"
	RealtimeErrorCodeAudioUnsupported    RealtimeErrorCode = "AUDIO_FORMAT_UNSUPPORTED"
	RealtimeErrorCodeAudioSequence       RealtimeErrorCode = "AUDIO_SEQUENCE_INVALID"
	RealtimeErrorCodeBackpressure        RealtimeErrorCode = "TRANSCRIPTION_BACKPRESSURE"
	RealtimeErrorCodeProviderUnavailable RealtimeErrorCode = "ASR_PROVIDER_UNAVAILABLE"
	RealtimeErrorCodeProviderRejected    RealtimeErrorCode = "ASR_PROVIDER_REJECTED"
	RealtimeErrorCodeQuotaExceeded       RealtimeErrorCode = "MEETING_QUOTA_EXCEEDED"
	RealtimeErrorCodeDurationExceeded    RealtimeErrorCode = "MEETING_DURATION_EXCEEDED"
	RealtimeErrorCodeProtocol            RealtimeErrorCode = "TRANSCRIPTION_PROTOCOL_INVALID"
	RealtimeErrorCodeInternal            RealtimeErrorCode = "INTERNAL_ERROR"
)

type realtimeAudioRequest struct {
	MIMEType        string `json:"mimeType"`
	SampleRate      int32  `json:"sampleRate"`
	Channels        int32  `json:"channels"`
	ChunkDurationMS int32  `json:"chunkDurationMs"`
}

type realtimeStartRequest struct {
	Version       int32                     `json:"version"`
	Type          RealtimeClientMessageType `json:"type"`
	SessionTicket string                    `json:"sessionTicket"`
	Audio         realtimeAudioRequest      `json:"audio"`
}

type realtimeAudioHeader struct {
	Version    int32                     `json:"version"`
	Type       RealtimeClientMessageType `json:"type"`
	SequenceNo int64                     `json:"sequenceNo"`
	CapturedAt int64                     `json:"capturedAt"`
	MIMEType   string                    `json:"mimeType"`
}

type RealtimeControlMessage struct {
	Version        int32                     `json:"version"`
	Type           RealtimeClientMessageType `json:"type"`
	SentAt         int64                     `json:"sentAt,omitempty"`
	LastSequenceNo int64                     `json:"lastSequenceNo,omitempty"`
}

type RealtimeAcceptedAudio struct {
	Format     RealtimeAudioFormat `json:"format"`
	SampleRate int32               `json:"sampleRate"`
	Channels   int32               `json:"channels"`
}

type RealtimeSessionReady struct {
	Version             int32                     `json:"version"`
	Type                RealtimeServerMessageType `json:"type"`
	SessionID           string                    `json:"sessionId"`
	AcceptedAudio       RealtimeAcceptedAudio     `json:"acceptedAudio"`
	GrantedAudioSeconds int64                     `json:"grantedAudioSeconds"`
}

type RealtimeACK struct {
	Version         int32                     `json:"version"`
	Type            RealtimeServerMessageType `json:"type"`
	ACKSequenceNo   int64                     `json:"ackSequenceNo"`
	AcceptedAudioMS int64                     `json:"acceptedAudioMs"`
}

type RealtimePong struct {
	Version int32                     `json:"version"`
	Type    RealtimeServerMessageType `json:"type"`
	SentAt  int64                     `json:"sentAt"`
}

type RealtimeTranscriptSegment struct {
	Version       int32                     `json:"version"`
	Type          RealtimeServerMessageType `json:"type"`
	SegmentID     string                    `json:"segmentId"`
	SequenceNo    int64                     `json:"sequenceNo"`
	Revision      int32                     `json:"revision"`
	StartOffsetMS int64                     `json:"startOffsetMs"`
	EndOffsetMS   int64                     `json:"endOffsetMs"`
	SpeakerLabel  *string                   `json:"speakerLabel"`
	Language      RealtimeLanguage          `json:"language"`
	Content       string                    `json:"content"`
	IsFinal       bool                      `json:"isFinal"`
}

type RealtimeSessionFinished struct {
	Version                int32                     `json:"version"`
	Type                   RealtimeServerMessageType `json:"type"`
	LastACKSequenceNo      int64                     `json:"lastAckSequenceNo"`
	FinalSegmentSequenceNo int64                     `json:"finalSegmentSequenceNo"`
	AcceptedAudioMS        int64                     `json:"acceptedAudioMs"`
	FinishReason           RealtimeFinishReason      `json:"finishReason"`
}

type RealtimeError struct {
	Version           int32                     `json:"version"`
	Type              RealtimeServerMessageType `json:"type"`
	Code              RealtimeErrorCode         `json:"code"`
	Message           string                    `json:"message"`
	Retryable         bool                      `json:"retryable"`
	LastACKSequenceNo int64                     `json:"lastAckSequenceNo"`
}

type RealtimeTranscriptionService struct {
	uc           *biz.TranscriptionUsecase
	replayWindow int64
}

func NewRealtimeTranscriptionService(uc *biz.TranscriptionUsecase, replayWindow int32) (*RealtimeTranscriptionService, error) {
	if uc == nil {
		return nil, fmt.Errorf("transcription use case is required")
	}
	if replayWindow <= 0 || replayWindow > 1_024 {
		return nil, fmt.Errorf("realtime replay window must be between 1 and 1024")
	}
	return &RealtimeTranscriptionService{uc: uc, replayWindow: int64(replayWindow)}, nil
}

type RealtimeSession struct {
	uc           *biz.TranscriptionUsecase
	claims       biz.TicketClaims
	spec         biz.AudioSpec
	replayWindow int64
	lastACK      int64
}

func (s *RealtimeTranscriptionService) Start(ctx context.Context, payload []byte) (*RealtimeSession, *RealtimeSessionReady, error) {
	var request realtimeStartRequest
	if err := decodeStrictJSON(payload, &request); err != nil {
		return nil, nil, protocolError("start message must be valid JSON", err)
	}
	if request.Version != realtimeProtocolVersion || request.Type != RealtimeClientMessageTypeStart || strings.TrimSpace(request.SessionTicket) == "" {
		return nil, nil, protocolError("start message is invalid", nil)
	}
	claims, err := s.uc.ConsumeTicket(ctx, request.SessionTicket)
	if err != nil {
		return nil, nil, err
	}
	if err := validateRealtimeAudio(request.Audio, claims.Audio); err != nil {
		return nil, nil, err
	}
	stored, err := s.uc.Get(ctx, claims.SessionID, claims.MeetingID)
	if err != nil {
		return nil, nil, err
	}
	if stored.UserID != claims.UserID || int64(stored.GrantedAudioDuration.Duration()/time.Second) != claims.GrantedAudioSeconds {
		return nil, nil, kratoserrors.Unauthorized("TRANSCRIPTION_TICKET_INVALID", "transcription session ticket constraints are invalid")
	}
	started, err := s.uc.Start(ctx, claims.SessionID, claims.MeetingID)
	if err != nil {
		return nil, nil, err
	}
	session := &RealtimeSession{uc: s.uc, claims: *claims, spec: claims.Audio, replayWindow: s.replayWindow, lastACK: started.LastAudioSequence}
	ready := &RealtimeSessionReady{
		Version: realtimeProtocolVersion, Type: RealtimeServerMessageTypeSessionReady, SessionID: started.ID,
		AcceptedAudio:       RealtimeAcceptedAudio{Format: RealtimeAudioFormatPCM, SampleRate: claims.Audio.SampleRate, Channels: claims.Audio.Channels},
		GrantedAudioSeconds: claims.GrantedAudioSeconds,
	}
	return session, ready, nil
}

func (s *RealtimeSession) SessionID() string { return s.claims.SessionID }

func (s *RealtimeSession) LastACKSequence() int64 { return s.lastACK }

func (s *RealtimeSession) Events() (<-chan biz.TranscriptEvent, error) {
	return s.uc.TranscriptEvents(s.claims.SessionID)
}

func (s *RealtimeSession) ParseControl(payload []byte) (*RealtimeControlMessage, error) {
	var message RealtimeControlMessage
	if err := decodeStrictJSON(payload, &message); err != nil {
		return nil, protocolError("control message must be valid JSON", err)
	}
	if message.Version != realtimeProtocolVersion {
		return nil, protocolError("control message version is unsupported", nil)
	}
	switch message.Type {
	case RealtimeClientMessageTypePing:
		if message.SentAt <= 0 {
			return nil, protocolError("ping sentAt must be positive", nil)
		}
	case RealtimeClientMessageTypeFinish:
		if message.LastSequenceNo < 0 || message.LastSequenceNo > s.lastACK {
			return nil, sequenceError("finish sequence exceeds the last accepted sequence")
		}
	default:
		return nil, protocolError("control message type is unsupported", nil)
	}
	return &message, nil
}

func (s *RealtimeSession) Pong(sentAt int64) *RealtimePong {
	return &RealtimePong{Version: realtimeProtocolVersion, Type: RealtimeServerMessageTypePong, SentAt: sentAt}
}

func (s *RealtimeSession) PushAudio(ctx context.Context, payload []byte) (*RealtimeACK, bool, error) {
	newline := bytes.IndexByte(payload, '\n')
	if newline <= 0 || newline > maxAudioHeaderBytes || newline == len(payload)-1 {
		return nil, false, protocolError("audio chunk framing is invalid", nil)
	}
	var header realtimeAudioHeader
	if err := decodeStrictJSON(payload[:newline], &header); err != nil {
		return nil, false, protocolError("audio chunk header is invalid", err)
	}
	if header.Version != realtimeProtocolVersion || header.Type != RealtimeClientMessageTypeAudioChunk {
		return nil, false, protocolError("audio chunk header is invalid", nil)
	}
	if header.MIMEType != pcmMIMEType {
		return nil, false, audioFormatError()
	}
	if header.SequenceNo <= 0 || header.SequenceNo > maxRealtimeAudioSequence || header.CapturedAt <= 0 {
		return nil, false, sequenceError("audio chunk sequence or timestamp is invalid")
	}
	if header.SequenceNo <= s.lastACK && s.lastACK-header.SequenceNo >= s.replayWindow {
		return nil, false, sequenceError("audio chunk is outside the replay window")
	}
	data := payload[newline+1:]
	result, err := s.uc.PushAudio(ctx, biz.AudioChunk{
		SessionID: s.claims.SessionID, Sequence: header.SequenceNo,
		CapturedAt: time.UnixMilli(header.CapturedAt).UTC(), Data: data,
	})
	if err != nil {
		return nil, false, err
	}
	if result == nil || result.Session == nil {
		return nil, false, kratoserrors.InternalServer("TRANSCRIPTION_RESULT_INVALID", "transcription result is invalid")
	}
	accepted := result.Session.LastAudioSequence == header.SequenceNo
	if result.Session.LastAudioSequence > s.lastACK {
		s.lastACK = result.Session.LastAudioSequence
	}
	acceptedDuration, err := result.Session.AcceptedAudioDuration(s.spec)
	if err != nil {
		return nil, false, kratoserrors.InternalServer("TRANSCRIPTION_USAGE_INVALID", "transcription usage is invalid").WithCause(err)
	}
	ack := &RealtimeACK{
		Version: realtimeProtocolVersion, Type: RealtimeServerMessageTypeACK, ACKSequenceNo: s.lastACK,
		AcceptedAudioMS: int64(acceptedDuration.Duration() / time.Millisecond),
	}
	if !accepted && !result.Duplicate {
		return nil, result.LimitReached, nil
	}
	return ack, result.LimitReached, nil
}

func (s *RealtimeSession) Transcript(event biz.TranscriptEvent) (*RealtimeTranscriptSegment, error) {
	if err := event.Validate(); err != nil {
		return nil, kratoserrors.InternalServer("TRANSCRIPT_EVENT_INVALID", "transcript event is invalid").WithCause(err)
	}
	if event.Segment.SessionID != "" && event.Segment.SessionID != s.claims.SessionID {
		return nil, kratoserrors.InternalServer("TRANSCRIPT_EVENT_INVALID", "transcript event session is invalid")
	}
	language, err := realtimeLanguage(event.Segment.Language)
	if err != nil {
		return nil, err
	}
	return &RealtimeTranscriptSegment{
		Version: realtimeProtocolVersion, Type: RealtimeServerMessageTypeTranscriptSegment, SegmentID: event.Segment.ID,
		SequenceNo: event.Segment.Sequence, Revision: event.Revision,
		StartOffsetMS: int64(event.Segment.StartOffset / time.Millisecond), EndOffsetMS: int64(event.Segment.EndOffset / time.Millisecond),
		SpeakerLabel: nil, Language: language, Content: event.Segment.Content,
		IsFinal: event.Type == biz.TranscriptEventTypeFinal,
	}, nil
}

func (s *RealtimeSession) Finish(ctx context.Context, reason RealtimeFinishReason, finalSequence int64) (*RealtimeSessionFinished, error) {
	if !validFinishReason(reason) {
		return nil, kratoserrors.InternalServer("TRANSCRIPTION_FINISH_REASON_INVALID", "transcription finish reason is invalid")
	}
	finished, err := s.uc.Finish(ctx, s.claims.SessionID)
	if err != nil {
		return nil, err
	}
	accepted, err := finished.AcceptedAudioDuration(s.spec)
	if err != nil {
		return nil, kratoserrors.InternalServer("TRANSCRIPTION_USAGE_INVALID", "transcription usage is invalid").WithCause(err)
	}
	return &RealtimeSessionFinished{
		Version: realtimeProtocolVersion, Type: RealtimeServerMessageTypeSessionFinished, LastACKSequenceNo: finished.LastAudioSequence,
		FinalSegmentSequenceNo: finalSequence, AcceptedAudioMS: int64(accepted.Duration() / time.Millisecond), FinishReason: reason,
	}, nil
}

func (s *RealtimeSession) Cancel(ctx context.Context) error {
	_, err := s.uc.Cancel(ctx, s.claims.SessionID, s.claims.MeetingID)
	return err
}

func RealtimeErrorFrom(err error, lastACK int64) *RealtimeError {
	reason := kratoserrors.Reason(err)
	code := RealtimeErrorCodeInternal
	message := "实时转写发生内部错误"
	retryable := false
	switch reason {
	case "TRANSCRIPTION_TICKET_INVALID":
		code, message = RealtimeErrorCodeTicketInvalid, "实时转写凭证无效或已使用"
	case "TRANSCRIPTION_SESSION_EXPIRED":
		code, message = RealtimeErrorCodeSessionExpired, "实时转写会话已过期"
	case "AUDIO_FORMAT_UNSUPPORTED":
		code, message = RealtimeErrorCodeAudioUnsupported, "音频格式不受支持"
	case "AUDIO_SEQUENCE_INVALID":
		code, message = RealtimeErrorCodeAudioSequence, "音频序号无效"
	case "TRANSCRIPTION_BACKPRESSURE":
		code, message, retryable = RealtimeErrorCodeBackpressure, "实时转写繁忙，请暂停发送", true
	case "ASR_PROVIDER_REJECTED":
		code, message = RealtimeErrorCodeProviderRejected, "实时转写请求被服务提供方拒绝"
	case "MEETING_QUOTA_EXCEEDED":
		code, message = RealtimeErrorCodeQuotaExceeded, "会议可用转写时长已耗尽"
	case "MEETING_DURATION_EXCEEDED":
		code, message = RealtimeErrorCodeDurationExceeded, "会议已达到最长时长"
	case "TRANSCRIPTION_PROTOCOL_INVALID", "AUDIO_CHUNK_INVALID":
		code, message = RealtimeErrorCodeProtocol, "实时转写消息格式无效"
	case "ASR_START_FAILED", "ASR_PUSH_FAILED", "ASR_FINISH_FAILED", "ASR_PROVIDER_UNAVAILABLE":
		code, message, retryable = RealtimeErrorCodeProviderUnavailable, "实时转写服务暂时不可用，请稍后重试", true
	default:
		if stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
			code, message, retryable = RealtimeErrorCodeProviderUnavailable, "实时转写连接已中断", true
		}
	}
	return &RealtimeError{Version: realtimeProtocolVersion, Type: RealtimeServerMessageTypeError, Code: code, Message: message, Retryable: retryable, LastACKSequenceNo: lastACK}
}

func RealtimeQuotaExceeded(lastACK int64) *RealtimeError {
	return RealtimeErrorFrom(kratoserrors.Conflict("MEETING_QUOTA_EXCEEDED", "meeting quota exceeded"), lastACK)
}

func RealtimeProtocolViolation(message string) error {
	return protocolError(message, nil)
}

func validateRealtimeAudio(request realtimeAudioRequest, spec biz.AudioSpec) error {
	if request.MIMEType != pcmMIMEType || request.SampleRate != spec.SampleRate || request.Channels != spec.Channels ||
		request.ChunkDurationMS <= 0 || time.Duration(request.ChunkDurationMS)*time.Millisecond != spec.ChunkDuration {
		return audioFormatError()
	}
	return nil
}

func realtimeLanguage(language biz.MeetingLanguage) (RealtimeLanguage, error) {
	switch language {
	case biz.MeetingLanguageAuto:
		return RealtimeLanguageAuto, nil
	case biz.MeetingLanguageZhCN:
		return RealtimeLanguageZh, nil
	case biz.MeetingLanguageEnUS:
		return RealtimeLanguageEn, nil
	default:
		return "", kratoserrors.InternalServer("TRANSCRIPT_LANGUAGE_INVALID", "transcript language is invalid")
	}
}

func validFinishReason(reason RealtimeFinishReason) bool {
	switch reason {
	case RealtimeFinishReasonClientFinished, RealtimeFinishReasonQuotaExhausted,
		RealtimeFinishReasonCancelled, RealtimeFinishReasonExpired:
		return true
	default:
		return false
	}
}

func decodeStrictJSON(payload []byte, target any) error {
	if len(payload) == 0 {
		return io.ErrUnexpectedEOF
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !stderrors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func protocolError(message string, cause error) error {
	err := kratoserrors.BadRequest("TRANSCRIPTION_PROTOCOL_INVALID", message)
	if cause != nil {
		return err.WithCause(cause)
	}
	return err
}

func audioFormatError() error {
	return kratoserrors.BadRequest("AUDIO_FORMAT_UNSUPPORTED", "audio format is unsupported")
}

func sequenceError(message string) error {
	return kratoserrors.BadRequest("AUDIO_SEQUENCE_INVALID", message)
}

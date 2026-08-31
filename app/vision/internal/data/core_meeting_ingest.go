package data

import (
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/google/uuid"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	maxCoreGRPCRequestTimeout = 30 * time.Second
	maxCoreTranscriptBatch    = 100
)

type CoreMeetingIngestGateway struct {
	client        coreMeetingIngestClient
	summaryClient coreMeetingSummaryClient
	conn          *grpc.ClientConn
}

type coreMeetingIngestClient interface {
	AppendFinalTranscriptSegments(context.Context, *meetingv1.AppendFinalTranscriptSegmentsRequest, ...grpc.CallOption) (*meetingv1.AppendFinalTranscriptSegmentsResponse, error)
	ReportTranscriptionUsage(context.Context, *meetingv1.ReportTranscriptionUsageRequest, ...grpc.CallOption) (*meetingv1.ReportTranscriptionUsageResponse, error)
	CompleteTranscription(context.Context, *meetingv1.CompleteTranscriptionRequest, ...grpc.CallOption) (*meetingv1.CompleteTranscriptionResponse, error)
	FailTranscription(context.Context, *meetingv1.FailTranscriptionRequest, ...grpc.CallOption) (*meetingv1.FailTranscriptionResponse, error)
}

type coreMeetingSummaryClient interface {
	GetMeetingTranscriptSnapshot(context.Context, *meetingv1.GetMeetingTranscriptSnapshotRequest, ...grpc.CallOption) (*meetingv1.GetMeetingTranscriptSnapshotResponse, error)
	CompleteMeetingSummary(context.Context, *meetingv1.CompleteMeetingSummaryRequest, ...grpc.CallOption) (*meetingv1.CompleteMeetingSummaryResponse, error)
	FailMeetingSummary(context.Context, *meetingv1.FailMeetingSummaryRequest, ...grpc.CallOption) (*meetingv1.FailMeetingSummaryResponse, error)
}

var _ biz.CoreMeetingIngestSink = (*CoreMeetingIngestGateway)(nil)
var _ biz.CoreMeetingSummarySink = (*CoreMeetingIngestGateway)(nil)

func NewCoreMeetingIngestGateway(ctx context.Context, cfg *conf.CoreGRPCClient) (*CoreMeetingIngestGateway, error) {
	if err := ValidateCoreGRPCClientConfig(cfg); err != nil {
		return nil, err
	}
	opts := []kratosgrpc.ClientOption{
		kratosgrpc.WithEndpoint(cfg.GetEndpoint()),
		kratosgrpc.WithTimeout(cfg.GetRequestTimeout().AsDuration()),
	}
	if cfg.GetTlsEnabled() {
		opts = append(opts, kratosgrpc.WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.GetTlsServerName()}))
	}
	conn, err := kratosgrpc.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create core gRPC client: %w", err)
	}
	client := meetingv1.NewMeetingIngestInternalServiceClient(conn)
	return &CoreMeetingIngestGateway{client: client, summaryClient: client, conn: conn}, nil
}

func ValidateCoreGRPCClientConfig(cfg *conf.CoreGRPCClient) error {
	if cfg == nil {
		return fmt.Errorf("core gRPC client config is required")
	}
	endpoint := strings.TrimSpace(cfg.GetEndpoint())
	if endpoint == "" || strings.ContainsAny(endpoint, " \t\r\n") {
		return fmt.Errorf("core gRPC endpoint is invalid")
	}
	if cfg.GetRequestTimeout() == nil {
		return fmt.Errorf("core gRPC request timeout is required")
	}
	timeout := cfg.GetRequestTimeout().AsDuration()
	if timeout <= 0 || timeout > maxCoreGRPCRequestTimeout {
		return fmt.Errorf("core gRPC request timeout must be between zero and %s", maxCoreGRPCRequestTimeout)
	}
	if !cfg.GetTlsEnabled() && !cfg.GetAllowInsecure() {
		return fmt.Errorf("core gRPC plaintext requires allow_insecure")
	}
	if cfg.GetTlsEnabled() {
		serverName := strings.TrimSpace(cfg.GetTlsServerName())
		if serverName == "" || strings.ContainsAny(serverName, " \t\r\n/:") {
			return fmt.Errorf("core gRPC TLS server name is invalid")
		}
	}
	return nil
}

func (g *CoreMeetingIngestGateway) AppendFinalTranscriptSegments(ctx context.Context, eventID string, session *biz.TranscriptionSession, segments []biz.TranscriptSegment) error {
	if session == nil {
		return fmt.Errorf("transcription session is required")
	}
	for start, batchIndex := 0, 0; start < len(segments); start, batchIndex = start+maxCoreTranscriptBatch, batchIndex+1 {
		end := start + maxCoreTranscriptBatch
		if end > len(segments) {
			end = len(segments)
		}
		mapped := make([]*meetingv1.TranscriptSegment, 0, end-start)
		for index := start; index < end; index++ {
			segment, err := visionTranscriptSegmentToProto(segments[index])
			if err != nil {
				return err
			}
			mapped = append(mapped, segment)
		}
		batchID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(eventID+":"+strconv.Itoa(batchIndex))).String()
		requestCtx := coreIngestContext(ctx, session, eventID)
		if _, err := g.client.AppendFinalTranscriptSegments(requestCtx, &meetingv1.AppendFinalTranscriptSegmentsRequest{
			MeetingId: session.MeetingID, SessionId: session.ID, BatchId: batchID, Segments: mapped,
		}); err != nil {
			return mapCoreIngestError(err)
		}
	}
	return nil
}

func (g *CoreMeetingIngestGateway) ReportTranscriptionUsage(ctx context.Context, session *biz.TranscriptionSession, accepted time.Duration, observedAt time.Time) error {
	if session == nil {
		return fmt.Errorf("transcription session is required")
	}
	_, err := g.client.ReportTranscriptionUsage(coreIngestContext(ctx, session, ""), &meetingv1.ReportTranscriptionUsageRequest{
		MeetingId: session.MeetingID, SessionId: session.ID, ReservationId: session.ReservationID,
		TotalAcceptedAudioDuration: durationpb.New(accepted), ObservedAt: timestamppb.New(observedAt),
	})
	return mapCoreIngestError(err)
}

func (g *CoreMeetingIngestGateway) CompleteTranscription(ctx context.Context, session *biz.TranscriptionSession, accepted, providerUsage time.Duration, completedAt time.Time) error {
	if session == nil {
		return fmt.Errorf("transcription session is required")
	}
	_, err := g.client.CompleteTranscription(coreIngestContext(ctx, session, ""), &meetingv1.CompleteTranscriptionRequest{
		MeetingId: session.MeetingID, SessionId: session.ID, ReservationId: session.ReservationID,
		TotalAcceptedAudioDuration: durationpb.New(accepted), ProviderUsageDuration: durationpb.New(providerUsage),
		CompletedAt: timestamppb.New(completedAt),
	})
	return mapCoreIngestError(err)
}

func (g *CoreMeetingIngestGateway) FailTranscription(ctx context.Context, session *biz.TranscriptionSession, accepted time.Duration, reason biz.TranscriptionFailureReason, failedAt time.Time) error {
	if session == nil {
		return fmt.Errorf("transcription session is required")
	}
	mappedReason, err := transcriptionFailureReasonToProto(reason)
	if err != nil {
		return err
	}
	_, err = g.client.FailTranscription(coreIngestContext(ctx, session, ""), &meetingv1.FailTranscriptionRequest{
		MeetingId: session.MeetingID, SessionId: session.ID, ReservationId: session.ReservationID,
		TotalAcceptedAudioDuration: durationpb.New(accepted), Reason: mappedReason, FailedAt: timestamppb.New(failedAt),
	})
	return mapCoreIngestError(err)
}

func (g *CoreMeetingIngestGateway) GetTranscriptSnapshot(ctx context.Context, meetingID string, revision int64) (*biz.MeetingTranscriptSnapshot, error) {
	if g.summaryClient == nil {
		return nil, fmt.Errorf("core meeting summary service is unavailable")
	}
	response, err := g.summaryClient.GetMeetingTranscriptSnapshot(ctx, &meetingv1.GetMeetingTranscriptSnapshotRequest{
		MeetingId: meetingID, TranscriptRevision: revision,
	})
	if err != nil {
		return nil, mapCoreIngestError(err)
	}
	if response == nil || response.GetTranscriptRevision() != revision {
		return nil, fmt.Errorf("core returned an invalid transcript snapshot")
	}
	language, err := visionMeetingLanguageFromProto(response.GetLanguage())
	if err != nil {
		return nil, err
	}
	segments := make([]biz.TranscriptSegment, 0, len(response.GetSegments()))
	for _, input := range response.GetSegments() {
		if input == nil || input.GetStartOffset() == nil || input.GetEndOffset() == nil || input.GetCreatedAt() == nil || input.GetConfidence() < 0 || input.GetConfidence() > 1 {
			return nil, fmt.Errorf("core transcript snapshot contains an invalid segment")
		}
		segmentLanguage, err := visionMeetingLanguageFromProto(input.GetLanguage())
		if err != nil {
			return nil, err
		}
		segments = append(segments, biz.TranscriptSegment{
			ID: input.GetSegmentId(), Sequence: input.GetSequenceNo(),
			StartOffset: input.GetStartOffset().AsDuration(), EndOffset: input.GetEndOffset().AsDuration(),
			SpeakerLabel: input.GetSpeakerLabel(), Content: input.GetContent(), Language: segmentLanguage,
			Confidence: float64(input.GetConfidence()), CreatedAt: input.GetCreatedAt().AsTime(),
		})
	}
	return &biz.MeetingTranscriptSnapshot{
		MeetingID: meetingID, Language: language, TranscriptRevision: revision, Segments: segments,
	}, nil
}

func (g *CoreMeetingIngestGateway) CompleteMeetingSummary(ctx context.Context, job *biz.MeetingSummaryJob) error {
	if job == nil || job.Result == nil {
		return fmt.Errorf("meeting summary delivery requires a result")
	}
	actionItems := make([]*meetingv1.MeetingActionItem, 0, len(job.Result.ActionItems))
	for _, item := range job.Result.ActionItems {
		actionItems = append(actionItems, &meetingv1.MeetingActionItem{
			Assignee: item.Assignee, Task: item.Task, DueText: item.DueText,
			Status: meetingv1.MeetingActionItemStatus_MEETING_ACTION_ITEM_STATUS_PENDING,
		})
	}
	if g.summaryClient == nil {
		return fmt.Errorf("core meeting summary service is unavailable")
	}
	_, err := g.summaryClient.CompleteMeetingSummary(ctx, &meetingv1.CompleteMeetingSummaryRequest{
		MeetingId: job.MeetingID, Version: job.Version, SourceTranscriptRevision: job.SourceTranscriptRevision,
		Summary: &meetingv1.MeetingSummary{
			MeetingId: job.MeetingID, Version: job.Version, SourceTranscriptRevision: job.SourceTranscriptRevision,
			Topic: job.Result.Topic, Abstract: job.Result.Abstract,
			KeyDiscussions: job.Result.KeyDiscussions, Decisions: job.Result.Decisions,
			ActionItems: actionItems, Risks: job.Result.Risks,
			Provider: string(job.Provider), ModelName: job.ModelName, PromptVersion: job.PromptVersion,
			InputTokens: job.Result.InputTokens, OutputTokens: job.Result.OutputTokens,
			GeneratedAt: timestamppb.New(job.Result.GeneratedAt),
		},
	})
	return mapCoreIngestError(err)
}

func (g *CoreMeetingIngestGateway) FailMeetingSummary(ctx context.Context, job *biz.MeetingSummaryJob, failedAt time.Time) error {
	if job == nil {
		return fmt.Errorf("meeting summary failure delivery requires a job")
	}
	reason, err := meetingSummaryFailureReasonToProto(job.FailureReason)
	if err != nil {
		return err
	}
	if g.summaryClient == nil {
		return fmt.Errorf("core meeting summary service is unavailable")
	}
	_, err = g.summaryClient.FailMeetingSummary(ctx, &meetingv1.FailMeetingSummaryRequest{
		MeetingId: job.MeetingID, Version: job.Version, SourceTranscriptRevision: job.SourceTranscriptRevision,
		Reason: reason, FailedAt: timestamppb.New(failedAt),
	})
	return mapCoreIngestError(err)
}

func (g *CoreMeetingIngestGateway) Close() error {
	if g == nil || g.conn == nil {
		return nil
	}
	return g.conn.Close()
}

func coreIngestContext(ctx context.Context, session *biz.TranscriptionSession, eventID string) context.Context {
	pairs := []string{"x-meeting-id", session.MeetingID, "x-transcription-session-id", session.ID}
	if eventID != "" {
		pairs = append(pairs, "x-outbox-event-id", eventID)
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}

func visionTranscriptSegmentToProto(segment biz.TranscriptSegment) (*meetingv1.TranscriptSegment, error) {
	language, err := visionMeetingLanguageToProto(segment.Language)
	if err != nil {
		return nil, err
	}
	confidence := float32(segment.Confidence)
	return &meetingv1.TranscriptSegment{
		SegmentId: segment.ID, SequenceNo: segment.Sequence,
		StartOffset: durationpb.New(segment.StartOffset), EndOffset: durationpb.New(segment.EndOffset),
		SpeakerLabel: segment.SpeakerLabel, Content: segment.Content, Language: language,
		Confidence: &confidence, CreatedAt: timestamppb.New(segment.CreatedAt),
	}, nil
}

func visionMeetingLanguageToProto(language biz.MeetingLanguage) (meetingv1.MeetingLanguage, error) {
	switch language {
	case biz.MeetingLanguageAuto:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_AUTO, nil
	case biz.MeetingLanguageZhCN:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_ZH_CN, nil
	case biz.MeetingLanguageEnUS:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_EN_US, nil
	default:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_UNSPECIFIED, fmt.Errorf("transcription language is invalid")
	}
}

func visionMeetingLanguageFromProto(language meetingv1.MeetingLanguage) (biz.MeetingLanguage, error) {
	switch language {
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_AUTO:
		return biz.MeetingLanguageAuto, nil
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_ZH_CN:
		return biz.MeetingLanguageZhCN, nil
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_EN_US:
		return biz.MeetingLanguageEnUS, nil
	default:
		return "", fmt.Errorf("meeting language is invalid")
	}
}

func meetingSummaryFailureReasonToProto(reason biz.MeetingSummaryFailureReason) (meetingv1.MeetingSummaryFailureReason, error) {
	switch reason {
	case biz.MeetingSummaryFailureReasonProviderUnavailable:
		return meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_PROVIDER_UNAVAILABLE, nil
	case biz.MeetingSummaryFailureReasonProviderRejected:
		return meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_PROVIDER_REJECTED, nil
	case biz.MeetingSummaryFailureReasonOutputInvalid:
		return meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_OUTPUT_INVALID, nil
	case biz.MeetingSummaryFailureReasonTranscriptInvalid:
		return meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_TRANSCRIPT_INVALID, nil
	case biz.MeetingSummaryFailureReasonTimeout:
		return meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_TIMEOUT, nil
	case biz.MeetingSummaryFailureReasonInternal:
		return meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_INTERNAL, nil
	default:
		return meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_UNSPECIFIED, fmt.Errorf("meeting summary failure reason is invalid")
	}
}

func transcriptionFailureReasonToProto(reason biz.TranscriptionFailureReason) (meetingv1.TranscriptionFailureReason, error) {
	switch reason {
	case biz.TranscriptionFailureReasonProviderUnavailable:
		return meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_PROVIDER_UNAVAILABLE, nil
	case biz.TranscriptionFailureReasonProviderRejected:
		return meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_PROVIDER_REJECTED, nil
	case biz.TranscriptionFailureReasonAudioInvalid:
		return meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_AUDIO_INVALID, nil
	case biz.TranscriptionFailureReasonTimeout:
		return meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_TIMEOUT, nil
	case biz.TranscriptionFailureReasonQuotaExhausted:
		return meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_QUOTA_EXHAUSTED, nil
	case biz.TranscriptionFailureReasonCancelled:
		return meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_CANCELLED, nil
	case biz.TranscriptionFailureReasonInternal:
		return meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_INTERNAL, nil
	default:
		return meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_UNSPECIFIED, fmt.Errorf("transcription failure reason is invalid")
	}
}

func mapCoreIngestError(err error) error {
	if err == nil || stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch status.Code(err) {
	case codes.InvalidArgument, codes.NotFound, codes.FailedPrecondition, codes.AlreadyExists:
		return kratoserrors.Conflict("CORE_TRANSCRIPTION_REJECTED", "core rejected transcription delivery").WithCause(err)
	case codes.DeadlineExceeded:
		return kratoserrors.GatewayTimeout("CORE_INGEST_TIMEOUT", "core transcription ingest timed out").WithCause(err)
	case codes.Unavailable:
		return kratoserrors.ServiceUnavailable("CORE_INGEST_UNAVAILABLE", "core transcription ingest is unavailable").WithCause(err)
	default:
		return kratoserrors.ServiceUnavailable("CORE_INGEST_FAILED", "core transcription ingest failed").WithCause(err)
	}
}

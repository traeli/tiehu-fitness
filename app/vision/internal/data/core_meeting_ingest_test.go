package data

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
)

type fakeCoreMeetingIngestClient struct {
	appendRequests   []*meetingv1.AppendFinalTranscriptSegmentsRequest
	reportRequests   []*meetingv1.ReportTranscriptionUsageRequest
	completeRequests []*meetingv1.CompleteTranscriptionRequest
	failRequests     []*meetingv1.FailTranscriptionRequest
}

func (c *fakeCoreMeetingIngestClient) AppendFinalTranscriptSegments(_ context.Context, request *meetingv1.AppendFinalTranscriptSegmentsRequest, _ ...grpc.CallOption) (*meetingv1.AppendFinalTranscriptSegmentsResponse, error) {
	c.appendRequests = append(c.appendRequests, request)
	return &meetingv1.AppendFinalTranscriptSegmentsResponse{}, nil
}

func (c *fakeCoreMeetingIngestClient) ReportTranscriptionUsage(_ context.Context, request *meetingv1.ReportTranscriptionUsageRequest, _ ...grpc.CallOption) (*meetingv1.ReportTranscriptionUsageResponse, error) {
	c.reportRequests = append(c.reportRequests, request)
	return &meetingv1.ReportTranscriptionUsageResponse{}, nil
}

func (c *fakeCoreMeetingIngestClient) CompleteTranscription(_ context.Context, request *meetingv1.CompleteTranscriptionRequest, _ ...grpc.CallOption) (*meetingv1.CompleteTranscriptionResponse, error) {
	c.completeRequests = append(c.completeRequests, request)
	return &meetingv1.CompleteTranscriptionResponse{}, nil
}

func (c *fakeCoreMeetingIngestClient) FailTranscription(_ context.Context, request *meetingv1.FailTranscriptionRequest, _ ...grpc.CallOption) (*meetingv1.FailTranscriptionResponse, error) {
	c.failRequests = append(c.failRequests, request)
	return &meetingv1.FailTranscriptionResponse{}, nil
}

func TestCoreMeetingIngestGatewayBatchesFinalSegmentsWithStableIDs(t *testing.T) {
	client := &fakeCoreMeetingIngestClient{}
	gateway := &CoreMeetingIngestGateway{client: client}
	session := &biz.TranscriptionSession{ID: uuid.NewString(), MeetingID: uuid.NewString(), ReservationID: uuid.NewString()}
	eventID := uuid.NewString()
	segments := make([]biz.TranscriptSegment, 101)
	for index := range segments {
		segments[index] = biz.TranscriptSegment{
			ID: uuid.NewString(), SessionID: session.ID, Sequence: int64(index + 1),
			StartOffset: time.Duration(index) * time.Second, EndOffset: time.Duration(index+1) * time.Second,
			Content: "segment", Language: biz.MeetingLanguageZhCN, Confidence: 0.9, CreatedAt: time.Now().UTC(),
		}
	}
	if err := gateway.AppendFinalTranscriptSegments(context.Background(), eventID, session, segments); err != nil {
		t.Fatal(err)
	}
	if len(client.appendRequests) != 2 || len(client.appendRequests[0].GetSegments()) != 100 || len(client.appendRequests[1].GetSegments()) != 1 ||
		client.appendRequests[0].GetBatchId() == client.appendRequests[1].GetBatchId() {
		t.Fatalf("append requests = %#v", client.appendRequests)
	}
	firstBatchID := client.appendRequests[0].GetBatchId()
	client.appendRequests = nil
	if err := gateway.AppendFinalTranscriptSegments(context.Background(), eventID, session, segments); err != nil {
		t.Fatal(err)
	}
	if client.appendRequests[0].GetBatchId() != firstBatchID {
		t.Fatalf("retry batch ID = %s, want %s", client.appendRequests[0].GetBatchId(), firstBatchID)
	}
}

func TestValidateCoreGRPCClientConfig(t *testing.T) {
	valid := &conf.CoreGRPCClient{
		Endpoint: "dns:///127.0.0.1:9000", RequestTimeout: durationpb.New(time.Second), AllowInsecure: true,
	}
	if err := ValidateCoreGRPCClientConfig(valid); err != nil {
		t.Fatalf("valid config error = %v", err)
	}
	insecureDisabled := &conf.CoreGRPCClient{
		Endpoint: valid.GetEndpoint(), RequestTimeout: durationpb.New(valid.GetRequestTimeout().AsDuration()),
	}
	if err := ValidateCoreGRPCClientConfig(insecureDisabled); err == nil {
		t.Fatal("plaintext config without allow_insecure was accepted")
	}
}

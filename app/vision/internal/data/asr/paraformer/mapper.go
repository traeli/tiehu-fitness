package paraformer

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

type eventMapper struct {
	taskID        string
	sessionID     string
	language      biz.MeetingLanguage
	finalSequence int64
	revisions     map[string]int32
	finalized     map[string]struct{}
}

func newEventMapper(taskID string, session *biz.TranscriptionSession) *eventMapper {
	return &eventMapper{
		taskID: taskID, sessionID: session.ID, language: session.Language,
		revisions: make(map[string]int32), finalized: make(map[string]struct{}),
	}
}

func languageHints(language biz.MeetingLanguage) ([]string, error) {
	switch language {
	case biz.MeetingLanguageAuto:
		return nil, nil
	case biz.MeetingLanguageZhCN:
		return []string{"zh"}, nil
	case biz.MeetingLanguageEnUS:
		return []string{"en"}, nil
	default:
		return nil, fmt.Errorf("unsupported meeting language %q", language)
	}
}

func (m *eventMapper) mapResult(event *serverEvent) (*biz.TranscriptEvent, error) {
	if event == nil || event.Payload.Output.Sentence == nil {
		return nil, fmt.Errorf("result-generated event is missing sentence")
	}
	sentence := event.Payload.Output.Sentence
	if sentence.Heartbeat {
		return nil, nil
	}
	content := strings.TrimSpace(sentence.Text)
	const maxSessionMilliseconds = int64((24 * time.Hour) / time.Millisecond)
	if sentence.BeginTime < 0 || sentence.BeginTime > maxSessionMilliseconds || len(content) > 262_144 {
		return nil, fmt.Errorf("result-generated sentence is invalid")
	}
	endTime := sentence.BeginTime
	if sentence.EndTime != nil {
		endTime = *sentence.EndTime
	}
	if endTime < sentence.BeginTime || endTime > maxSessionMilliseconds {
		return nil, fmt.Errorf("result-generated sentence offsets are invalid")
	}
	// Paraformer may emit an empty, non-heartbeat result while VAD is still
	// observing initial silence. It carries no transcript revision and is safe
	// to ignore; rejecting it tears down an otherwise healthy live session.
	if content == "" {
		return nil, nil
	}
	logicalID := strconv.FormatInt(sentence.BeginTime, 10)
	if sentence.SentenceID != nil {
		if *sentence.SentenceID < 0 {
			return nil, fmt.Errorf("result-generated sentence id is invalid")
		}
		logicalID = "sentence-" + strconv.FormatInt(*sentence.SentenceID, 10)
	}
	segmentID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(m.taskID+":"+logicalID)).String()
	if _, ok := m.finalized[segmentID]; ok {
		return nil, fmt.Errorf("provider revised a finalized sentence")
	}
	if m.revisions[segmentID] == math.MaxInt32 || m.finalSequence == math.MaxInt64 {
		return nil, fmt.Errorf("provider transcript sequence is out of range")
	}
	revision := m.revisions[segmentID] + 1
	m.revisions[segmentID] = revision
	sequence := m.finalSequence + 1
	eventType := biz.TranscriptEventTypePartial
	if sentence.SentenceEnd {
		eventType = biz.TranscriptEventTypeFinal
		m.finalSequence = sequence
		m.finalized[segmentID] = struct{}{}
	}
	usage := time.Duration(0)
	if event.Payload.Usage != nil {
		if event.Payload.Usage.Duration < 0 || event.Payload.Usage.Duration > int64((24*time.Hour)/time.Second) {
			return nil, fmt.Errorf("provider usage duration is invalid")
		}
		usage = time.Duration(event.Payload.Usage.Duration) * time.Second
	}
	mapped := &biz.TranscriptEvent{
		Type: eventType,
		Segment: biz.TranscriptSegment{
			ID: segmentID, SessionID: m.sessionID, Sequence: sequence,
			StartOffset: time.Duration(sentence.BeginTime) * time.Millisecond,
			EndOffset:   time.Duration(endTime) * time.Millisecond,
			Content:     content, Language: m.language, Confidence: 0, CreatedAt: time.Now().UTC(),
		},
		Revision: revision, ProviderUsageDuration: usage,
	}
	if err := mapped.Validate(); err != nil {
		return nil, err
	}
	return mapped, nil
}

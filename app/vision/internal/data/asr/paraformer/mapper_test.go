package paraformer

import (
	"testing"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

func TestEventMapperIgnoresEmptyVADResult(t *testing.T) {
	taskID := uuid.NewString()
	mapper := newEventMapper(taskID, &biz.TranscriptionSession{
		ID: uuid.NewString(), Language: biz.MeetingLanguageZhCN,
	})
	event := &serverEvent{}
	event.Header.TaskID = taskID
	event.Header.Event = "result-generated"
	event.Payload.Output.Sentence = &providerSentence{
		BeginTime: 0, Text: "", Heartbeat: false, SentenceEnd: false,
	}

	mapped, err := mapper.mapResult(event)
	if err != nil {
		t.Fatalf("mapResult() error = %v", err)
	}
	if mapped != nil {
		t.Fatalf("mapResult() = %#v, want nil", mapped)
	}
}

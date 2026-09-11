package paraformer

import (
	"encoding/json"
	"testing"
)

func TestNewRunTaskUsesModelSpecificParameters(t *testing.T) {
	tests := []struct {
		name             string
		model            string
		wantPhraseID     string
		wantVocabularyID string
		wantHints        []string
		absentJSONFields []string
	}{
		{
			name: "v1 uses phrase id and omits v2 language hints", model: paraformerRealtimeV1,
			wantPhraseID: "temporary-phrase", absentJSONFields: []string{"vocabulary_id", "language_hints"},
		},
		{
			name: "v2 keeps vocabulary id and language hints", model: "paraformer-realtime-v2",
			wantVocabularyID: "temporary-phrase", wantHints: []string{"zh"}, absentJSONFields: []string{"phrase_id"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := newRunTask("task-id", Config{Model: test.model, VocabularyID: "temporary-phrase"}, []string{"zh"})
			parameters := task.Payload.Parameters
			if parameters.PhraseID != test.wantPhraseID || parameters.VocabularyID != test.wantVocabularyID {
				t.Fatalf("newRunTask() identifiers = phrase %q, vocabulary %q", parameters.PhraseID, parameters.VocabularyID)
			}
			if len(parameters.LanguageHints) != len(test.wantHints) ||
				(len(test.wantHints) > 0 && parameters.LanguageHints[0] != test.wantHints[0]) {
				t.Fatalf("newRunTask() language hints = %v, want %v", parameters.LanguageHints, test.wantHints)
			}
			encoded, err := json.Marshal(task)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			body, ok := payload["payload"].(map[string]any)
			if !ok {
				t.Fatal("encoded payload is missing")
			}
			encodedParameters, ok := body["parameters"].(map[string]any)
			if !ok {
				t.Fatal("encoded parameters are missing")
			}
			for _, field := range test.absentJSONFields {
				if _, exists := encodedParameters[field]; exists {
					t.Errorf("encoded v1 parameters unexpectedly contain %q", field)
				}
			}
		})
	}
}

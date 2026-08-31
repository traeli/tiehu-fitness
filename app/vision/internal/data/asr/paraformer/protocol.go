package paraformer

type taskHeader struct {
	Action    string `json:"action"`
	TaskID    string `json:"task_id"`
	Streaming string `json:"streaming"`
}

type runTask struct {
	Header  taskHeader     `json:"header"`
	Payload runTaskPayload `json:"payload"`
}

type runTaskPayload struct {
	TaskGroup  string            `json:"task_group"`
	Task       string            `json:"task"`
	Function   string            `json:"function"`
	Model      string            `json:"model"`
	Parameters runTaskParameters `json:"parameters"`
	Input      struct{}          `json:"input"`
}

type runTaskParameters struct {
	Format                          string   `json:"format"`
	SampleRate                      int32    `json:"sample_rate"`
	VocabularyID                    string   `json:"vocabulary_id,omitempty"`
	LanguageHints                   []string `json:"language_hints,omitempty"`
	SemanticPunctuationEnabled      bool     `json:"semantic_punctuation_enabled"`
	PunctuationPredictionEnabled    bool     `json:"punctuation_prediction_enabled"`
	InverseTextNormalizationEnabled bool     `json:"inverse_text_normalization_enabled"`
	DisfluencyRemovalEnabled        bool     `json:"disfluency_removal_enabled"`
	Heartbeat                       bool     `json:"heartbeat"`
}

type finishTask struct {
	Header  taskHeader        `json:"header"`
	Payload finishTaskPayload `json:"payload"`
}

type finishTaskPayload struct {
	Input struct{} `json:"input"`
}

type serverEvent struct {
	Header struct {
		TaskID       string `json:"task_id"`
		Event        string `json:"event"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	} `json:"header"`
	Payload struct {
		Output struct {
			Sentence *providerSentence `json:"sentence"`
		} `json:"output"`
		Usage *providerUsage `json:"usage"`
	} `json:"payload"`
}

type providerSentence struct {
	SentenceID  *int64 `json:"sentence_id"`
	BeginTime   int64  `json:"begin_time"`
	EndTime     *int64 `json:"end_time"`
	Text        string `json:"text"`
	Heartbeat   bool   `json:"heartbeat"`
	SentenceEnd bool   `json:"sentence_end"`
}

type providerUsage struct {
	Duration int64 `json:"duration"`
}

func newRunTask(taskID string, cfg Config, languageHints []string) runTask {
	message := runTask{
		Header: taskHeader{Action: "run-task", TaskID: taskID, Streaming: "duplex"},
		Payload: runTaskPayload{
			TaskGroup: "audio", Task: "asr", Function: "recognition", Model: cfg.Model,
			Parameters: runTaskParameters{
				Format: "pcm", SampleRate: 16_000, VocabularyID: cfg.VocabularyID, LanguageHints: languageHints,
				SemanticPunctuationEnabled: true, PunctuationPredictionEnabled: true,
				InverseTextNormalizationEnabled: true, DisfluencyRemovalEnabled: false, Heartbeat: true,
			},
		},
	}
	return message
}

func newFinishTask(taskID string) finishTask {
	return finishTask{Header: taskHeader{Action: "finish-task", TaskID: taskID, Streaming: "duplex"}}
}

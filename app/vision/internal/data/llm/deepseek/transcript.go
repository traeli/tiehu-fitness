package deepseek

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

const (
	maximumMergedTranscriptRunes = 800
	maximumMergeGap              = 2 * time.Second
)

type meetingSummaryPromptVersion uint8

const (
	meetingSummaryPromptVersionV1 meetingSummaryPromptVersion = iota + 1
	meetingSummaryPromptVersionV2
)

func parseMeetingSummaryPromptVersion(raw string) (meetingSummaryPromptVersion, error) {
	switch strings.TrimSpace(raw) {
	case "meeting-summary-v1":
		return meetingSummaryPromptVersionV1, nil
	case "meeting-summary-v2":
		return meetingSummaryPromptVersionV2, nil
	default:
		return 0, fmt.Errorf("unsupported DeepSeek meeting summary prompt version %q", raw)
	}
}

func (v meetingSummaryPromptVersion) String() string {
	switch v {
	case meetingSummaryPromptVersionV1:
		return "meeting-summary-v1"
	case meetingSummaryPromptVersionV2:
		return "meeting-summary-v2"
	default:
		return ""
	}
}

type transcriptSpeakerMode uint8

const (
	transcriptSpeakerModeUnknown transcriptSpeakerMode = iota + 1
	transcriptSpeakerModeLabeled
)

func (m transcriptSpeakerMode) String() string {
	switch m {
	case transcriptSpeakerModeUnknown:
		return "unknown_or_single"
	case transcriptSpeakerModeLabeled:
		return "distinct_labels"
	default:
		return ""
	}
}

func (m transcriptSpeakerMode) PromptContext() string {
	if m == transcriptSpeakerModeLabeled {
		return "转写包含可区分的说话人标签；标签仅用于组织内容，不得据此猜测真实身份。"
	}
	return "转写没有可靠的多人说话人标签，可能是单人讲话或混合音频；不得因此判定内容不足，也不得虚构说话人。"
}

type transcriptQuality struct {
	SourceSegments   int
	ValidSegments    int
	RenderedSegments int
	DistinctContents int
	ContentRunes     int
	DistinctSpeakers int
	SpeakerMode      transcriptSpeakerMode
	Duration         time.Duration
}

type preparedTranscript struct {
	Chunks  []string
	Quality transcriptQuality
}

type normalizedTranscriptSegment struct {
	StartOffset  time.Duration
	EndOffset    time.Duration
	SpeakerLabel string
	Content      string
}

func (p *Provider) prepareTranscript(snapshot *biz.MeetingTranscriptSnapshot) (*preparedTranscript, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("meeting transcript is required")
	}
	if p.promptVersion == meetingSummaryPromptVersionV1 {
		return p.prepareLegacyTranscript(snapshot)
	}
	return p.prepareSemanticTranscript(snapshot)
}

func (p *Provider) prepareLegacyTranscript(snapshot *biz.MeetingTranscriptSnapshot) (*preparedTranscript, error) {
	lines := make([]string, 0, len(snapshot.Segments))
	speakers := make(map[string]struct{})
	contents := make(map[string]struct{})
	contentRunes := 0
	for _, segment := range snapshot.Segments {
		content := strings.TrimSpace(segment.Content)
		line := fmt.Sprintf("[%s-%s] %s: %s\n", formatOffset(segment.StartOffset), formatOffset(segment.EndOffset),
			fallbackSpeaker(segment.SpeakerLabel), content)
		lines = append(lines, line)
		contentRunes += utf8.RuneCountInString(content)
		contents[content] = struct{}{}
		if speaker := strings.TrimSpace(segment.SpeakerLabel); speaker != "" {
			speakers[speaker] = struct{}{}
		}
	}
	chunks, err := p.chunkTranscriptLines(lines)
	if err != nil {
		return nil, err
	}
	return &preparedTranscript{
		Chunks: chunks,
		Quality: transcriptQuality{
			SourceSegments: len(snapshot.Segments), ValidSegments: len(snapshot.Segments),
			RenderedSegments: len(lines), DistinctContents: len(contents), ContentRunes: contentRunes,
			DistinctSpeakers: len(speakers), SpeakerMode: speakerModeForCount(len(speakers)),
			Duration: transcriptDuration(snapshot.Segments),
		},
	}, nil
}

func (p *Provider) prepareSemanticTranscript(snapshot *biz.MeetingTranscriptSnapshot) (*preparedTranscript, error) {
	valid := make([]normalizedTranscriptSegment, 0, len(snapshot.Segments))
	speakers := make(map[string]struct{})
	contents := make(map[string]struct{})
	contentRunes := 0
	for _, segment := range snapshot.Segments {
		content := normalizeTranscriptContent(segment.Content)
		if !hasMeaningfulTranscriptContent(content) || isFillerOnly(content) {
			continue
		}
		speaker := normalizeTranscriptContent(segment.SpeakerLabel)
		if speaker != "" {
			speakers[speaker] = struct{}{}
		}
		contents[content] = struct{}{}
		contentRunes += utf8.RuneCountInString(content)
		candidate := normalizedTranscriptSegment{
			StartOffset: segment.StartOffset, EndOffset: segment.EndOffset,
			SpeakerLabel: speaker, Content: content,
		}
		if len(valid) > 0 && isImmediateDuplicate(valid[len(valid)-1], candidate) {
			if candidate.EndOffset > valid[len(valid)-1].EndOffset {
				valid[len(valid)-1].EndOffset = candidate.EndOffset
			}
			continue
		}
		valid = append(valid, candidate)
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("meeting transcript contains no meaningful linguistic content")
	}

	speakerMode := speakerModeForCount(len(speakers))
	merged := mergeTranscriptSegments(valid, speakerMode)
	lines := make([]string, 0, len(merged))
	for _, segment := range merged {
		prefix := fmt.Sprintf("[%s-%s] ", formatOffset(segment.StartOffset), formatOffset(segment.EndOffset))
		if speakerMode == transcriptSpeakerModeLabeled && segment.SpeakerLabel != "" {
			prefix += segment.SpeakerLabel + ": "
		}
		lines = append(lines, prefix+segment.Content+"\n")
	}
	chunks, err := p.chunkTranscriptLines(lines)
	if err != nil {
		return nil, err
	}
	return &preparedTranscript{
		Chunks: chunks,
		Quality: transcriptQuality{
			SourceSegments: len(snapshot.Segments), ValidSegments: len(valid), RenderedSegments: len(merged),
			DistinctContents: len(contents), ContentRunes: contentRunes, DistinctSpeakers: len(speakers),
			SpeakerMode: speakerMode, Duration: transcriptDuration(snapshot.Segments),
		},
	}, nil
}

func (p *Provider) chunkTranscriptLines(lines []string) ([]string, error) {
	chunks := make([]string, 0, 8)
	var current strings.Builder
	currentRunes := 0
	for _, line := range lines {
		lineRunes := utf8.RuneCountInString(line)
		if lineRunes > p.config.MaxInputCharsPerChunk {
			return nil, fmt.Errorf("one transcript segment exceeds DeepSeek chunk limit")
		}
		if currentRunes > 0 && currentRunes+lineRunes > p.config.MaxInputCharsPerChunk {
			chunks = append(chunks, current.String())
			current.Reset()
			currentRunes = 0
		}
		current.WriteString(line)
		currentRunes += lineRunes
	}
	if currentRunes > 0 {
		chunks = append(chunks, current.String())
	}
	if len(chunks) == 0 || len(chunks) > p.config.MaxChunks {
		return nil, fmt.Errorf("meeting transcript requires %d chunks, outside configured limit", len(chunks))
	}
	return chunks, nil
}

func normalizeTranscriptContent(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func hasMeaningfulTranscriptContent(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

func isFillerOnly(value string) bool {
	var semantic strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			semantic.WriteRune(r)
		}
	}
	switch semantic.String() {
	case "嗯", "啊", "呃", "额", "哦", "唔", "喂", "em", "um", "uh":
		return true
	default:
		return false
	}
}

func isImmediateDuplicate(previous, current normalizedTranscriptSegment) bool {
	return previous.Content == current.Content && speakersCompatible(previous.SpeakerLabel, current.SpeakerLabel) &&
		current.StartOffset-previous.EndOffset <= maximumMergeGap
}

func mergeTranscriptSegments(segments []normalizedTranscriptSegment, mode transcriptSpeakerMode) []normalizedTranscriptSegment {
	merged := make([]normalizedTranscriptSegment, 0, len(segments))
	for _, segment := range segments {
		if len(merged) == 0 {
			merged = append(merged, segment)
			continue
		}
		previous := &merged[len(merged)-1]
		gap := segment.StartOffset - previous.EndOffset
		compatible := mode == transcriptSpeakerModeUnknown || speakersCompatible(previous.SpeakerLabel, segment.SpeakerLabel)
		combinedRunes := utf8.RuneCountInString(previous.Content) + 1 + utf8.RuneCountInString(segment.Content)
		if gap <= maximumMergeGap && compatible && combinedRunes <= maximumMergedTranscriptRunes {
			previous.Content = previous.Content + " " + segment.Content
			if segment.EndOffset > previous.EndOffset {
				previous.EndOffset = segment.EndOffset
			}
			continue
		}
		merged = append(merged, segment)
	}
	return merged
}

func speakersCompatible(first, second string) bool {
	return first == second || first == "" || second == ""
}

func speakerModeForCount(count int) transcriptSpeakerMode {
	if count >= 2 {
		return transcriptSpeakerModeLabeled
	}
	return transcriptSpeakerModeUnknown
}

func transcriptDuration(segments []biz.TranscriptSegment) time.Duration {
	if len(segments) == 0 {
		return 0
	}
	start := segments[0].StartOffset
	end := segments[0].EndOffset
	for _, segment := range segments[1:] {
		if segment.StartOffset < start {
			start = segment.StartOffset
		}
		if segment.EndOffset > end {
			end = segment.EndOffset
		}
	}
	if end < start {
		return 0
	}
	return end - start
}

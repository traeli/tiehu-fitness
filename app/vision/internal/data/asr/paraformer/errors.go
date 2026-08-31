package paraformer

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

type ErrorCode string

const (
	ErrorCodeHandshake      ErrorCode = "HANDSHAKE_FAILED"
	ErrorCodeProtocol       ErrorCode = "PROTOCOL_ERROR"
	ErrorCodeTaskFailed     ErrorCode = "TASK_FAILED"
	ErrorCodeConnectionLost ErrorCode = "CONNECTION_LOST"
	ErrorCodeTimeout        ErrorCode = "TIMEOUT"
	ErrorCodeBackpressure   ErrorCode = "BACKPRESSURE"
	ErrorCodeCancelled      ErrorCode = "CANCELLED"
	ErrorCodeInternal       ErrorCode = "INTERNAL"
)

var safeProviderCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Error deliberately omits provider messages and credentials from its public
// string. The wrapped cause is retained for errors.Is and structured logging.
type Error struct {
	Code         ErrorCode
	ProviderCode string
	cause        error
}

func (e *Error) Error() string {
	if e == nil {
		return "paraformer error"
	}
	if e.ProviderCode != "" {
		return fmt.Sprintf("paraformer %s (%s)", strings.ToLower(string(e.Code)), e.ProviderCode)
	}
	return fmt.Sprintf("paraformer %s", strings.ToLower(string(e.Code)))
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	switch target {
	case biz.ErrASRBackpressure:
		return e.Code == ErrorCodeBackpressure
	case biz.ErrASRProviderRejected:
		return e.Code == ErrorCodeTaskFailed || e.Code == ErrorCodeProtocol
	case biz.ErrASRProviderUnavailable:
		return e.Code == ErrorCodeHandshake || e.Code == ErrorCodeConnectionLost || e.Code == ErrorCodeTimeout || e.Code == ErrorCodeInternal
	default:
		return false
	}
}

func providerError(code ErrorCode, cause error) error {
	if code == ErrorCodeBackpressure {
		return &Error{Code: code, cause: cause}
	}
	if errors.Is(cause, context.Canceled) {
		return &Error{Code: ErrorCodeCancelled, cause: context.Canceled}
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return &Error{Code: ErrorCodeTimeout, cause: context.DeadlineExceeded}
	}
	return &Error{Code: code, cause: cause}
}

func taskFailure(code string) error {
	providerCode := "UNKNOWN"
	if safeProviderCode.MatchString(code) {
		providerCode = code
	}
	return &Error{Code: ErrorCodeTaskFailed, ProviderCode: providerCode}
}

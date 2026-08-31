package paraformer

import (
	"errors"
	"testing"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

func TestProviderErrorDomainCategories(t *testing.T) {
	tests := []struct {
		code ErrorCode
		want error
	}{
		{ErrorCodeBackpressure, biz.ErrASRBackpressure},
		{ErrorCodeTaskFailed, biz.ErrASRProviderRejected},
		{ErrorCodeHandshake, biz.ErrASRProviderUnavailable},
		{ErrorCodeConnectionLost, biz.ErrASRProviderUnavailable},
		{ErrorCodeTimeout, biz.ErrASRProviderUnavailable},
	}
	for _, test := range tests {
		err := &Error{Code: test.code}
		if !errors.Is(err, test.want) {
			t.Fatalf("Error{%s} does not match %v", test.code, test.want)
		}
	}
	if errors.Is(&Error{Code: ErrorCodeTaskFailed}, biz.ErrASRBackpressure) {
		t.Fatal("task failure must not be classified as backpressure")
	}
}

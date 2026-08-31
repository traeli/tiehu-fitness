package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	testUToolsPluginID     = "plugin-test"
	testUToolsPluginSecret = "0123456789abcdef0123456789abcdef"
)

func TestUToolsClientVerifiesSignedIdentity(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	provider, err := NewUToolsProvider(validUToolsConfig("https://open.u-tools.cn/baseinfo"))
	if err != nil {
		t.Fatal(err)
	}
	client, ok := provider.(*uToolsClient)
	if !ok {
		t.Fatal("NewUToolsProvider() returned unexpected implementation")
	}
	client.now = func() time.Time { return now }
	client.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/baseinfo" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		params := cloneValues(r.URL.Query())
		gotSign := params.Get("sign")
		params.Del("sign")
		if gotSign != signUToolsValues(params, testUToolsPluginSecret) {
			t.Error("request signature is invalid")
		}
		if params.Get("plugin_id") != testUToolsPluginID || len(params.Get("access_token")) != 32 {
			t.Errorf("request params = %v", params)
		}
		const avatar = "https://res.u-tools.cn/avatar.png"
		const nickname = "会议用户"
		const openID = "stable-open-id"
		resource := map[string]any{
			"avatar": avatar, "member": 1,
			"nickname": nickname, "open_id": openID, "timestamp": now.Unix(),
		}
		values := url.Values{
			"avatar": {avatar}, "member": {"1"},
			"nickname": {nickname}, "open_id": {openID},
			"timestamp": {strconv.FormatInt(now.Unix(), 10)},
		}
		body, err := json.Marshal(map[string]any{
			"resource": resource, "sign": signUToolsValues(values, testUToolsPluginSecret),
		})
		if err != nil {
			return nil, err
		}
		return jsonResponse(r, body), nil
	})
	identity, err := client.VerifyTemporaryToken(context.Background(), "12345678901234567890123456789012")
	if err != nil {
		t.Fatal(err)
	}
	if identity.OpenID != "stable-open-id" || identity.PluginID != testUToolsPluginID || !identity.Member {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestUToolsClientRejectsInvalidResponseSignature(t *testing.T) {
	now := time.Now().UTC()
	provider, err := NewUToolsProvider(validUToolsConfig("https://open.u-tools.cn/baseinfo"))
	if err != nil {
		t.Fatal(err)
	}
	client, ok := provider.(*uToolsClient)
	if !ok {
		t.Fatal("NewUToolsProvider() returned unexpected implementation")
	}
	client.now = func() time.Time { return now }
	client.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := json.Marshal(map[string]any{
			"resource": map[string]any{
				"avatar": "", "member": 0, "nickname": "user", "open_id": "open-id", "timestamp": now.Unix(),
			},
			"sign": "00",
		})
		if err != nil {
			return nil, err
		}
		return jsonResponse(r, body), nil
	})
	_, err = client.VerifyTemporaryToken(context.Background(), "12345678901234567890123456789012")
	if kratoserrors.Reason(err) != "UTOOLS_RESPONSE_SIGNATURE_INVALID" {
		t.Fatalf("VerifyTemporaryToken() error = %v", err)
	}
}

func TestNewUToolsProviderRejectsUnsafeEndpoint(t *testing.T) {
	tests := []string{
		"http://open.u-tools.cn/baseinfo",
		"https://open.u-tools.cn/other",
		"https://user:pass@open.u-tools.cn/baseinfo",
		"https://open.u-tools.cn/baseinfo?redirect=evil",
	}
	for _, endpoint := range tests {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := NewUToolsProvider(validUToolsConfig(endpoint)); err == nil {
				t.Fatal("NewUToolsProvider() expected error")
			}
		})
	}
}

func TestUToolsClientMapsProviderFailuresAndCancellation(t *testing.T) {
	provider, err := NewUToolsProvider(validUToolsConfig("https://open.u-tools.cn/baseinfo"))
	if err != nil {
		t.Fatal(err)
	}
	client, ok := provider.(*uToolsClient)
	if !ok {
		t.Fatal("NewUToolsProvider() returned unexpected implementation")
	}
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnprocessableEntity,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"message":"invalid"}`))),
			Request:    request,
		}, nil
	})
	if _, err := client.VerifyTemporaryToken(context.Background(), "12345678901234567890123456789012"); kratoserrors.Reason(err) != "UTOOLS_LOGIN_FAILED" {
		t.Fatalf("provider rejection error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})
	if _, err := client.VerifyTemporaryToken(ctx, "12345678901234567890123456789012"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func validUToolsConfig(endpoint string) *conf.UTools {
	return &conf.UTools{
		PluginId: testUToolsPluginID, PluginSecret: testUToolsPluginSecret,
		BaseInfoEndpoint: endpoint, RequestTimeout: durationpb.New(time.Second),
		ResponseMaxAge: durationpb.New(10 * time.Minute), AllowTestEndpoint: true,
	}
}

func cloneValues(source url.Values) url.Values {
	cloned := make(url.Values, len(source))
	for key, values := range source {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(request *http.Request, body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    request,
	}
}

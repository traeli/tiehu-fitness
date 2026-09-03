package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
)

func TestCORSFilterAllowsConfiguredPreflight(t *testing.T) {
	filter, err := newCORSFilter(&conf.HTTPCORS{AllowedOrigins: []string{"http://127.0.0.1:5173"}})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := filter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequest(http.MethodOptions, "/v1/meeting-quota", nil)
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if called {
		t.Fatal("preflight unexpectedly reached the application handler")
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type" {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
}

func TestCORSFilterAllowsUToolsRendererOrigin(t *testing.T) {
	filter, err := newCORSFilter(&conf.HTTPCORS{AllowedOrigins: []string{"utools://*"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := filter(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusAccepted)
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/meeting-quota", nil)
	request.Header.Set("Origin", "utools://zxm1gvtr")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "utools://zxm1gvtr" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}

func TestCORSFilterAllowsPackagedUToolsFileOrigin(t *testing.T) {
	filter, err := newCORSFilter(&conf.HTTPCORS{AllowedOrigins: []string{"file://"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := filter(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusAccepted)
	}))
	request := httptest.NewRequest(http.MethodOptions, "/v1/auth/utools/login", nil)
	request.Header.Set("Origin", "file://")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "file://" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}

func TestCORSFilterRejectsUnknownOriginAndHeader(t *testing.T) {
	filter, err := newCORSFilter(&conf.HTTPCORS{AllowedOrigins: []string{"http://127.0.0.1:5173"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := filter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("rejected request reached the application handler")
	}))
	tests := []struct {
		name   string
		origin string
		header string
	}{
		{name: "unknown origin", origin: "https://attacker.example"},
		{name: "unconfigured file origin", origin: "file://"},
		{name: "file path is not an origin", origin: "file:///tmp/index.html"},
		{name: "unknown header", origin: "http://127.0.0.1:5173", header: "X-Unsafe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, "/v1/meeting-quota", nil)
			request.Header.Set("Origin", tt.origin)
			request.Header.Set("Access-Control-Request-Method", http.MethodGet)
			if tt.header != "" {
				request.Header.Set("Access-Control-Request-Headers", tt.header)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
		})
	}
}

func TestCORSFilterRequiresExplicitSafeOrigins(t *testing.T) {
	tests := []*conf.HTTPCORS{
		nil,
		{},
		{AllowedOrigins: []string{"*"}},
		{AllowedOrigins: []string{"https://example.com/path"}},
	}
	for _, config := range tests {
		if _, err := newCORSFilter(config); err == nil {
			t.Fatalf("newCORSFilter(%v) error = nil", config)
		}
	}
}

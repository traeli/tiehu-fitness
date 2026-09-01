package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

const corsPreflightMaxAgeSeconds = "600"

var corsAllowedMethods = map[string]struct{}{
	http.MethodGet: {}, http.MethodHead: {}, http.MethodPost: {}, http.MethodPut: {},
	http.MethodPatch: {}, http.MethodDelete: {}, http.MethodOptions: {},
}

var corsAllowedHeaders = map[string]struct{}{
	http.CanonicalHeaderKey("Accept"):          {},
	http.CanonicalHeaderKey("Authorization"):   {},
	http.CanonicalHeaderKey("Content-Type"):    {},
	http.CanonicalHeaderKey("Idempotency-Key"): {},
}

type corsPolicy struct {
	exactOrigins       map[string]struct{}
	allowUToolsOrigins bool
}

func newCORSFilter(c *conf.HTTPCORS) (kratoshttp.FilterFunc, error) {
	policy, err := newCORSPolicy(c)
	if err != nil {
		return nil, err
	}
	return policy.filter, nil
}

func newCORSPolicy(c *conf.HTTPCORS) (*corsPolicy, error) {
	if c == nil || len(c.GetAllowedOrigins()) == 0 {
		return nil, fmt.Errorf("HTTP CORS allowed origins are required")
	}
	policy := &corsPolicy{exactOrigins: make(map[string]struct{}, len(c.GetAllowedOrigins()))}
	for _, configuredOrigin := range c.GetAllowedOrigins() {
		origin := strings.TrimSpace(configuredOrigin)
		switch origin {
		case "":
			return nil, fmt.Errorf("HTTP CORS allowed origin must not be empty")
		case "*":
			return nil, fmt.Errorf("HTTP CORS wildcard origin is not allowed")
		case "utools://*":
			policy.allowUToolsOrigins = true
			continue
		case "null":
			policy.exactOrigins[origin] = struct{}{}
			continue
		}
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("HTTP CORS allowed origin %q is invalid", origin)
		}
		policy.exactOrigins[origin] = struct{}{}
	}
	return policy, nil
}

func (p *corsPolicy) filter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(response, request)
			return
		}
		if !p.allowsOrigin(origin) {
			http.Error(response, "origin is not allowed", http.StatusForbidden)
			return
		}

		response.Header().Add("Vary", "Origin")
		response.Header().Set("Access-Control-Allow-Origin", origin)
		requestedMethod := request.Header.Get("Access-Control-Request-Method")
		if request.Method != http.MethodOptions || requestedMethod == "" {
			next.ServeHTTP(response, request)
			return
		}

		method := strings.ToUpper(strings.TrimSpace(requestedMethod))
		if _, allowed := corsAllowedMethods[method]; !allowed {
			http.Error(response, "CORS request method is not allowed", http.StatusForbidden)
			return
		}
		requestedHeaders, ok := allowedCORSRequestHeaders(request.Header.Get("Access-Control-Request-Headers"))
		if !ok {
			http.Error(response, "CORS request header is not allowed", http.StatusForbidden)
			return
		}
		response.Header().Add("Vary", "Access-Control-Request-Method")
		response.Header().Add("Vary", "Access-Control-Request-Headers")
		response.Header().Set("Access-Control-Allow-Methods", method)
		if requestedHeaders != "" {
			response.Header().Set("Access-Control-Allow-Headers", requestedHeaders)
		}
		response.Header().Set("Access-Control-Max-Age", corsPreflightMaxAgeSeconds)
		response.WriteHeader(http.StatusNoContent)
	})
}

func (p *corsPolicy) allowsOrigin(origin string) bool {
	if _, allowed := p.exactOrigins[origin]; allowed {
		return true
	}
	if !p.allowUToolsOrigins {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "utools" && parsed.Host != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

func allowedCORSRequestHeaders(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return "", true
	}
	parts := strings.Split(raw, ",")
	headers := make([]string, 0, len(parts))
	for _, part := range parts {
		header := http.CanonicalHeaderKey(strings.TrimSpace(part))
		if header == "" {
			return "", false
		}
		if _, allowed := corsAllowedHeaders[header]; !allowed {
			return "", false
		}
		headers = append(headers, header)
	}
	return strings.Join(headers, ", "), true
}

package data

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	maxASRSessionTimeout = 24 * time.Hour
	maxASRIOTimeout      = 2 * time.Minute
	maxASRProbeTimeout   = time.Minute
	maxASRSessions       = int32(10_000)
	maxASRFrameBytes     = int32(65_536)
)

// ValidateASRRuntimeConfig validates provider-neutral resource bounds. The
// explicit local fake development mode reuses these limits without requiring
// production provider credentials.
func ValidateASRRuntimeConfig(cfg *conf.ASR) error {
	if cfg == nil {
		return fmt.Errorf("asr config is required")
	}
	if _, err := boundedASRDuration("asr session_timeout", cfg.GetSessionTimeout(), maxASRSessionTimeout); err != nil {
		return err
	}
	for name, value := range map[string]*durationpb.Duration{
		"connect_timeout": cfg.GetConnectTimeout(),
		"read_timeout":    cfg.GetReadTimeout(),
		"write_timeout":   cfg.GetWriteTimeout(),
		"finish_timeout":  cfg.GetFinishTimeout(),
	} {
		if _, err := boundedASRDuration("asr "+name, value, maxASRIOTimeout); err != nil {
			return err
		}
	}
	if cfg.GetMaxConcurrentSessions() <= 0 || cfg.GetMaxConcurrentSessions() > maxASRSessions {
		return fmt.Errorf("asr max_concurrent_sessions must be between 1 and %d", maxASRSessions)
	}
	if cfg.GetMaxAudioFrameBytes() <= 0 || cfg.GetMaxAudioFrameBytes() > maxASRFrameBytes {
		return fmt.Errorf("asr max_audio_frame_bytes must be between 1 and %d", maxASRFrameBytes)
	}
	probe := cfg.GetStartupProbe()
	if probe == nil {
		return fmt.Errorf("asr startup_probe config is required")
	}
	if probe.GetEnabled() {
		if _, err := boundedASRDuration("asr startup_probe timeout", probe.GetTimeout(), maxASRProbeTimeout); err != nil {
			return err
		}
	}

	return nil
}

func resolveBailianEndpoint(raw, workspaceID string, allowTest bool) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "wss://" + workspaceID + ".cn-beijing.maas.aliyuncs.com/api-ws/v1/inference"
	}
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Hostname() == "" {
		return "", fmt.Errorf("asr bailian endpoint is invalid")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", fmt.Errorf("asr bailian endpoint must not contain credentials, query, or fragment")
	}
	host := strings.ToLower(endpoint.Hostname())
	expectedHost := strings.ToLower(workspaceID) + ".cn-beijing.maas.aliyuncs.com"
	if endpoint.Scheme == "wss" && host == expectedHost && endpoint.Path == "/api-ws/v1/inference" {
		return endpoint.String(), nil
	}
	if allowTest && (endpoint.Scheme == "ws" || endpoint.Scheme == "wss") && isLoopbackHost(host) {
		return endpoint.String(), nil
	}
	return "", fmt.Errorf("asr bailian endpoint must use the configured workspace-specific WSS host or an explicitly enabled loopback test endpoint")
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func boundedASRDuration(name string, value *durationpb.Duration, maximum time.Duration) (time.Duration, error) {
	if value == nil {
		return 0, fmt.Errorf("%s is required", name)
	}
	duration := value.AsDuration()
	if duration <= 0 || duration > maximum {
		return 0, fmt.Errorf("%s must be greater than zero and at most %s", name, maximum)
	}
	return duration, nil
}

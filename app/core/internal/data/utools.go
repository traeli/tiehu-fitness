package data

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	maxUToolsRequestTimeout = 30 * time.Second
	maxUToolsResponseAge    = 10 * time.Minute
	maxUToolsResponseBytes  = 1 << 20
)

type uToolsClient struct {
	pluginID       string
	pluginSecret   string
	endpoint       *url.URL
	responseMaxAge time.Duration
	client         *http.Client
	now            func() time.Time
}

var _ biz.UToolsIdentityProvider = (*uToolsClient)(nil)

// NewUToolsProvider constructs the server-only adapter used to validate uTools
// temporary tokens. Empty credentials keep other login methods available but
// make UToolsLogin return UTOOLS_AUTH_NOT_CONFIGURED.
func NewUToolsProvider(cfg *conf.UTools) (biz.UToolsIdentityProvider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("utools config is required")
	}
	endpoint, err := validateUToolsEndpoint(cfg.GetBaseInfoEndpoint(), cfg.GetAllowTestEndpoint())
	if err != nil {
		return nil, err
	}
	requestTimeout, err := boundedUToolsDuration("utools request_timeout", cfg.GetRequestTimeout(), maxUToolsRequestTimeout)
	if err != nil {
		return nil, err
	}
	responseMaxAge, err := boundedUToolsDuration("utools response_max_age", cfg.GetResponseMaxAge(), maxUToolsResponseAge)
	if err != nil {
		return nil, err
	}
	return &uToolsClient{
		pluginID: strings.TrimSpace(cfg.GetPluginId()), pluginSecret: strings.TrimSpace(cfg.GetPluginSecret()),
		endpoint: endpoint, responseMaxAge: responseMaxAge, now: time.Now,
		client: &http.Client{
			Timeout: requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("utools redirects are not allowed")
			},
		},
	}, nil
}

func (c *uToolsClient) VerifyTemporaryToken(ctx context.Context, temporaryToken string) (*biz.UToolsIdentity, error) {
	if c == nil || c.client == nil || c.endpoint == nil || c.pluginID == "" || c.pluginSecret == "" {
		return nil, kratoserrors.ServiceUnavailable("UTOOLS_AUTH_NOT_CONFIGURED", "uTools authentication is not configured")
	}
	timestamp := strconv.FormatInt(c.now().Unix(), 10)
	params := url.Values{
		"access_token": {temporaryToken},
		"plugin_id":    {c.pluginID},
		"timestamp":    {timestamp},
	}
	params.Set("sign", signUToolsValues(params, c.pluginSecret))
	endpoint := *c.endpoint
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, kratoserrors.InternalServer("UTOOLS_REQUEST_FAILED", "failed to create uTools request").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, kratoserrors.ServiceUnavailable("UTOOLS_UNAVAILABLE", "uTools authentication is temporarily unavailable").WithCause(err)
	}
	defer func() {
		// The response body is bounded and fully consumed below; Close errors do
		// not change the already known authentication outcome.
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, kratoserrors.ServiceUnavailable("UTOOLS_UNAVAILABLE", "uTools authentication is temporarily unavailable")
		}
		return nil, kratoserrors.Unauthorized("UTOOLS_LOGIN_FAILED", "uTools temporary token is invalid or expired")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUToolsResponseBytes+1))
	if err != nil {
		return nil, kratoserrors.ServiceUnavailable("UTOOLS_INVALID_RESPONSE", "uTools returned an invalid response").WithCause(err)
	}
	if len(body) > maxUToolsResponseBytes {
		return nil, kratoserrors.ServiceUnavailable("UTOOLS_INVALID_RESPONSE", "uTools response exceeds the size limit")
	}
	var result struct {
		Resource struct {
			Avatar    string `json:"avatar"`
			Member    int    `json:"member"`
			Nickname  string `json:"nickname"`
			OpenID    string `json:"open_id"`
			Timestamp int64  `json:"timestamp"`
		} `json:"resource"`
		Sign string `json:"sign"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, kratoserrors.ServiceUnavailable("UTOOLS_INVALID_RESPONSE", "uTools returned an invalid response").WithCause(err)
	}
	if result.Resource.Member != 0 && result.Resource.Member != 1 {
		return nil, kratoserrors.ServiceUnavailable("UTOOLS_INVALID_RESPONSE", "uTools returned invalid membership data")
	}
	if !withinClockSkew(c.now(), time.Unix(result.Resource.Timestamp, 0), c.responseMaxAge) {
		return nil, kratoserrors.Unauthorized("UTOOLS_RESPONSE_EXPIRED", "uTools response timestamp is outside the allowed window")
	}
	responseValues := url.Values{
		"avatar":    {result.Resource.Avatar},
		"member":    {strconv.Itoa(result.Resource.Member)},
		"nickname":  {result.Resource.Nickname},
		"open_id":   {result.Resource.OpenID},
		"timestamp": {strconv.FormatInt(result.Resource.Timestamp, 10)},
	}
	if !validUToolsSignature(responseValues, result.Sign, c.pluginSecret) {
		return nil, kratoserrors.Unauthorized("UTOOLS_RESPONSE_SIGNATURE_INVALID", "uTools response signature is invalid")
	}
	return &biz.UToolsIdentity{
		PluginID: c.pluginID, OpenID: result.Resource.OpenID, Nickname: result.Resource.Nickname,
		AvatarURI: result.Resource.Avatar, Member: result.Resource.Member == 1,
	}, nil
}

func validateUToolsEndpoint(raw string, allowTest bool) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Hostname() == "" {
		return nil, fmt.Errorf("utools base_info_endpoint is invalid")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("utools base_info_endpoint must not contain credentials, query, or fragment")
	}
	if endpoint.Scheme == "https" && endpoint.Host == "open.u-tools.cn" && endpoint.Path == "/baseinfo" {
		return endpoint, nil
	}
	host := endpoint.Hostname()
	if allowTest && (endpoint.Scheme == "http" || endpoint.Scheme == "https") && isUToolsLoopback(host) {
		return endpoint, nil
	}
	return nil, fmt.Errorf("utools base_info_endpoint must be the official HTTPS endpoint or an explicitly enabled loopback test endpoint")
}

func isUToolsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func signUToolsValues(values url.Values, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(values.Encode()))
	return hex.EncodeToString(mac.Sum(nil))
}

func validUToolsSignature(values url.Values, rawSignature, secret string) bool {
	want, err := hex.DecodeString(signUToolsValues(values, secret))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(strings.TrimSpace(rawSignature))
	return err == nil && hmac.Equal(got, want)
}

func withinClockSkew(now, value time.Time, maximum time.Duration) bool {
	difference := now.Sub(value)
	if difference < 0 {
		difference = -difference
	}
	return difference <= maximum
}

func boundedUToolsDuration(name string, value *durationpb.Duration, maximum time.Duration) (time.Duration, error) {
	if value == nil {
		return 0, fmt.Errorf("%s is required", name)
	}
	duration := value.AsDuration()
	if duration <= 0 || duration > maximum {
		return 0, fmt.Errorf("%s must be greater than zero and at most %s", name, maximum)
	}
	return duration, nil
}

package data

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

type WechatClient struct {
	appID     string
	appSecret string
	client    *http.Client
}

func NewWechatProvider(c *conf.Wechat) biz.WechatProvider {
	client := &WechatClient{client: &http.Client{Timeout: 5 * time.Second}}
	if c != nil {
		client.appID = c.GetAppId()
		client.appSecret = c.GetAppSecret()
	}
	return client
}

func (c *WechatClient) ExchangeCode(ctx context.Context, code string) (*biz.WechatIdentity, error) {
	if c.appID == "" || c.appSecret == "" {
		return nil, errors.ServiceUnavailable("WECHAT_NOT_CONFIGURED", "WECHAT_APP_ID and WECHAT_APP_SECRET are required")
	}
	params := url.Values{
		"appid":      {c.appID},
		"secret":     {c.appSecret},
		"js_code":    {code},
		"grant_type": {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.weixin.qq.com/sns/jscode2session?"+params.Encode(), nil)
	if err != nil {
		return nil, errors.InternalServer("WECHAT_REQUEST_FAILED", err.Error())
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.ServiceUnavailable("WECHAT_UNAVAILABLE", err.Error())
	}
	defer resp.Body.Close()
	var result struct {
		OpenID     string `json:"openid"`
		UnionID    string `json:"unionid"`
		SessionKey string `json:"session_key"`
		ErrCode    int    `json:"errcode"`
		ErrMsg     string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.ServiceUnavailable("WECHAT_INVALID_RESPONSE", err.Error())
	}
	if result.ErrCode != 0 || result.OpenID == "" {
		return nil, errors.Unauthorized("WECHAT_LOGIN_FAILED", result.ErrMsg)
	}
	return &biz.WechatIdentity{AppID: c.appID, OpenID: result.OpenID, UnionID: result.UnionID, SessionKey: result.SessionKey}, nil
}

type MemoryUserRepo struct {
	mu          sync.RWMutex
	users       map[string]*biz.User
	openidUsers map[string]string
	sessions    map[string]*biz.Session
	profiles    map[string]*biz.FitnessProfile
	plans       map[string]*biz.WorkoutPlan
	streaks     map[string]int32
}

func NewUserRepo() biz.UserRepo {
	return &MemoryUserRepo{
		users: make(map[string]*biz.User), openidUsers: make(map[string]string),
		sessions: make(map[string]*biz.Session), profiles: make(map[string]*biz.FitnessProfile),
		plans: make(map[string]*biz.WorkoutPlan), streaks: make(map[string]int32),
	}
}

func (r *MemoryUserRepo) UpsertWechatUser(_ context.Context, identity *biz.WechatIdentity) (*biz.User, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	identityKey := identity.AppID + "|" + identity.OpenID
	if id, ok := r.openidUsers[identityKey]; ok {
		copyOfUser := *r.users[id]
		return &copyOfUser, false, nil
	}
	user := &biz.User{ID: uuid.NewString(), Status: "active"}
	r.users[user.ID] = user
	r.openidUsers[identityKey] = user.ID
	r.profiles[user.ID] = &biz.FitnessProfile{UserID: user.ID}
	copyOfUser := *user
	return &copyOfUser, true, nil
}

func (r *MemoryUserRepo) SaveSession(_ context.Context, session *biz.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyOfSession := *session
	r.sessions[session.RefreshToken] = &copyOfSession
	return nil
}

func (r *MemoryUserRepo) GetSessionByRefreshToken(_ context.Context, token string) (*biz.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.sessions[token]
	if !ok {
		return nil, errors.Unauthorized("REFRESH_TOKEN_INVALID", "refresh token is invalid")
	}
	copyOfSession := *session
	return &copyOfSession, nil
}

func (r *MemoryUserRepo) DeleteSession(_ context.Context, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, token)
	return nil
}

func (r *MemoryUserRepo) GetProfile(_ context.Context, userID string) (*biz.FitnessProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	profile, ok := r.profiles[userID]
	if !ok {
		return nil, errors.NotFound("FITNESS_PROFILE_NOT_FOUND", "fitness profile not found")
	}
	copyOfProfile := *profile
	copyOfProfile.AvailableEquipmentCodes = append([]string(nil), profile.AvailableEquipmentCodes...)
	copyOfProfile.InjuryNotes = append([]string(nil), profile.InjuryNotes...)
	return &copyOfProfile, nil
}

func (r *MemoryUserRepo) SaveProfile(_ context.Context, profile *biz.FitnessProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyOfProfile := *profile
	copyOfProfile.AvailableEquipmentCodes = append([]string(nil), profile.AvailableEquipmentCodes...)
	copyOfProfile.InjuryNotes = append([]string(nil), profile.InjuryNotes...)
	r.profiles[profile.UserID] = &copyOfProfile
	return nil
}

func (r *MemoryUserRepo) SavePlan(_ context.Context, plan *biz.WorkoutPlan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyOfPlan := *plan
	r.plans[plan.ID] = &copyOfPlan
	return nil
}

func (r *MemoryUserRepo) GetPlan(_ context.Context, userID, planID string) (*biz.WorkoutPlan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	plan, ok := r.plans[planID]
	if !ok || plan.UserID != userID {
		return nil, errors.NotFound("WORKOUT_PLAN_NOT_FOUND", "workout plan not found")
	}
	copyOfPlan := *plan
	return &copyOfPlan, nil
}

func (r *MemoryUserRepo) CheckIn(_ context.Context, userID string, _ time.Time) (int32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streaks[userID]++
	return r.streaks[userID], nil
}

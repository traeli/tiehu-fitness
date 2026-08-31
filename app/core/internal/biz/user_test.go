package biz_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

type fakeWechatProvider struct{}

func (fakeWechatProvider) ExchangeCode(context.Context, string) (*biz.WechatIdentity, error) {
	return &biz.WechatIdentity{AppID: "miniapp-1", OpenID: "openid-1"}, nil
}

type fakeUToolsProvider struct{}

func (fakeUToolsProvider) VerifyTemporaryToken(context.Context, string) (*biz.UToolsIdentity, error) {
	return &biz.UToolsIdentity{
		PluginID: "plugin-1", OpenID: "utools-open-id-1", Nickname: "uTools 用户",
		AvatarURI: "https://res.u-tools.cn/avatar.png", Member: true,
	}, nil
}

type fakePasswordHasher struct{}

func (fakePasswordHasher) Hash(password string) (string, error) {
	return "hash:" + password, nil
}

func (fakePasswordHasher) Verify(encodedHash, password string) (bool, error) {
	return encodedHash == "hash:"+password, nil
}

type fakeUserRepo struct {
	mu            sync.Mutex
	users         map[string]*biz.User
	openidUsers   map[string]string
	utoolsUsers   map[string]string
	passwordUsers map[string]*biz.PasswordUser
	sessions      map[string]*biz.Session
	revokedAccess map[string]bool
	profiles      map[string]*biz.FitnessProfile
	plans         map[string]*biz.WorkoutPlan
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{
		users: map[string]*biz.User{}, openidUsers: map[string]string{}, utoolsUsers: map[string]string{},
		passwordUsers: map[string]*biz.PasswordUser{},
		sessions:      map[string]*biz.Session{}, revokedAccess: map[string]bool{}, profiles: map[string]*biz.FitnessProfile{},
		plans: map[string]*biz.WorkoutPlan{},
	}
}

func (r *fakeUserRepo) UpsertUToolsUser(_ context.Context, identity *biz.UToolsIdentity) (*biz.User, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := identity.PluginID + "|" + identity.OpenID
	if userID, ok := r.utoolsUsers[key]; ok {
		user := r.users[userID]
		user.Nickname = identity.Nickname
		user.AvatarURI = identity.AvatarURI
		return user, false, nil
	}
	user := &biz.User{
		ID: uuid.NewString(), Nickname: identity.Nickname, AvatarURI: identity.AvatarURI, Status: biz.UserStatusActive,
	}
	r.users[user.ID] = user
	r.utoolsUsers[key] = user.ID
	r.profiles[user.ID] = &biz.FitnessProfile{UserID: user.ID}
	return user, true, nil
}

func (r *fakeUserRepo) UpsertWechatUser(_ context.Context, identity *biz.WechatIdentity) (*biz.User, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := identity.AppID + "|" + identity.OpenID
	if userID, ok := r.openidUsers[key]; ok {
		return r.users[userID], false, nil
	}
	user := &biz.User{ID: uuid.NewString(), Status: biz.UserStatusActive}
	r.users[user.ID] = user
	r.openidUsers[key] = user.ID
	r.profiles[user.ID] = &biz.FitnessProfile{UserID: user.ID}
	return user, true, nil
}

func (r *fakeUserRepo) CreatePasswordUser(_ context.Context, email, passwordHash, nickname string) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.passwordUsers[email]; exists {
		return nil, biz.ErrEmailAlreadyExists
	}
	user := &biz.User{ID: uuid.NewString(), Nickname: nickname, Status: biz.UserStatusActive}
	r.users[user.ID] = user
	r.passwordUsers[email] = &biz.PasswordUser{User: user, PasswordHash: passwordHash}
	r.profiles[user.ID] = &biz.FitnessProfile{UserID: user.ID}
	return user, nil
}

func (r *fakeUserRepo) GetPasswordUser(_ context.Context, email string) (*biz.PasswordUser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	passwordUser, exists := r.passwordUsers[email]
	if !exists {
		return nil, biz.ErrPasswordUserNotFound
	}
	return passwordUser, nil
}

func (r *fakeUserRepo) SaveSession(_ context.Context, session *biz.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[session.RefreshToken] = session
	return nil
}

func (r *fakeUserRepo) GetSessionByRefreshToken(_ context.Context, token string) (*biz.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[token]
	if !ok {
		return nil, kratoserrors.Unauthorized("REFRESH_TOKEN_INVALID", "refresh token is invalid")
	}
	return session, nil
}

func (r *fakeUserRepo) GetAccessSession(_ context.Context, token string) (*biz.AccessSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, session := range r.sessions {
		if session.AccessToken == token {
			return &biz.AccessSession{
				User: r.users[session.UserID], AccessExpiry: session.AccessExpiry, Revoked: r.revokedAccess[token],
			}, nil
		}
	}
	return nil, biz.ErrAccessTokenNotFound
}

func (r *fakeUserRepo) DeleteSession(_ context.Context, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, token)
	return nil
}

func (r *fakeUserRepo) GetProfile(_ context.Context, userID string) (*biz.FitnessProfile, error) {
	profile, ok := r.profiles[userID]
	if !ok {
		return nil, kratoserrors.NotFound("FITNESS_PROFILE_NOT_FOUND", "fitness profile not found")
	}
	return profile, nil
}

func (r *fakeUserRepo) SaveProfile(_ context.Context, profile *biz.FitnessProfile) error {
	r.profiles[profile.UserID] = profile
	return nil
}

func (r *fakeUserRepo) SavePlan(_ context.Context, plan *biz.WorkoutPlan) error {
	r.plans[plan.ID] = plan
	return nil
}

func (r *fakeUserRepo) GetPlan(_ context.Context, userID, planID string) (*biz.WorkoutPlan, error) {
	plan, ok := r.plans[planID]
	if !ok || plan.UserID != userID {
		return nil, kratoserrors.NotFound("WORKOUT_PLAN_NOT_FOUND", "workout plan not found")
	}
	return plan, nil
}

func (*fakeUserRepo) CheckIn(context.Context, string, time.Time) (int32, error) { return 1, nil }

func TestWechatLoginAutomaticallyRegistersOnce(t *testing.T) {
	uc := biz.NewUserUsecase(fakeWechatProvider{}, fakeUToolsProvider{}, fakePasswordHasher{}, newFakeUserRepo(), time.Hour, 24*time.Hour)
	first, err := uc.WechatLogin(context.Background(), "code-1", "device-1")
	if err != nil {
		t.Fatalf("WechatLogin() error = %v", err)
	}
	if !first.IsNewUser || !first.OnboardingRequired {
		t.Fatalf("first login = %#v", first)
	}
	second, err := uc.WechatLogin(context.Background(), "code-2", "device-1")
	if err != nil {
		t.Fatalf("WechatLogin() second error = %v", err)
	}
	if second.IsNewUser || second.User.ID != first.User.ID {
		t.Fatalf("second login = %#v", second)
	}
}

func TestUToolsLoginAutomaticallyRegistersOnce(t *testing.T) {
	repo := newFakeUserRepo()
	uc := biz.NewUserUsecase(fakeWechatProvider{}, fakeUToolsProvider{}, fakePasswordHasher{}, repo, time.Hour, 24*time.Hour)
	const temporaryToken = "12345678901234567890123456789012"
	first, err := uc.UToolsLogin(context.Background(), temporaryToken, "utools-device-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := uc.UToolsLogin(context.Background(), temporaryToken, "utools-device-1")
	if err != nil {
		t.Fatal(err)
	}
	if !first.IsNewUser || second.IsNewUser || first.User.ID != second.User.ID {
		t.Fatalf("uTools login results = (%#v, %#v)", first, second)
	}
	if first.User.Nickname != "uTools 用户" || first.User.AvatarURI == "" {
		t.Fatalf("uTools user = %#v", first.User)
	}
}

func TestUToolsLoginRejectsInvalidBoundaryInput(t *testing.T) {
	uc := biz.NewUserUsecase(fakeWechatProvider{}, fakeUToolsProvider{}, fakePasswordHasher{}, newFakeUserRepo(), time.Hour, 24*time.Hour)
	tests := []struct {
		name, token, deviceID, reason string
	}{
		{name: "missing token", deviceID: "device-1", reason: "UTOOLS_TEMPORARY_TOKEN_REQUIRED"},
		{name: "short token", token: "short", deviceID: "device-1", reason: "UTOOLS_TEMPORARY_TOKEN_INVALID"},
		{name: "invalid device", token: "12345678901234567890123456789012", deviceID: "device id", reason: "DEVICE_ID_INVALID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := uc.UToolsLogin(context.Background(), tt.token, tt.deviceID)
			if kratoserrors.Reason(err) != tt.reason {
				t.Fatalf("UToolsLogin() error = %v", err)
			}
		})
	}
}

func TestAuthenticateAccessTokenChecksExpiryAndRevocation(t *testing.T) {
	repo := newFakeUserRepo()
	uc := biz.NewUserUsecase(fakeWechatProvider{}, fakeUToolsProvider{}, fakePasswordHasher{}, repo, time.Hour, 24*time.Hour)
	login, err := uc.UToolsLogin(context.Background(), "12345678901234567890123456789012", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	user, err := uc.AuthenticateAccessToken(context.Background(), login.Session.AccessToken)
	if err != nil || user.ID != login.User.ID {
		t.Fatalf("AuthenticateAccessToken() = (%#v, %v)", user, err)
	}
	login.User.Status = biz.UserStatusDisabled
	if _, err := uc.AuthenticateAccessToken(context.Background(), login.Session.AccessToken); kratoserrors.Reason(err) != "USER_DISABLED" {
		t.Fatalf("disabled user error = %v", err)
	}
	login.User.Status = biz.UserStatusActive

	login.Session.AccessExpiry = time.Now().Add(-time.Second)
	if _, err := uc.AuthenticateAccessToken(context.Background(), login.Session.AccessToken); kratoserrors.Reason(err) != "ACCESS_TOKEN_EXPIRED" {
		t.Fatalf("expired token error = %v", err)
	}
	login.Session.AccessExpiry = time.Now().Add(time.Hour)
	repo.revokedAccess[login.Session.AccessToken] = true
	if _, err := uc.AuthenticateAccessToken(context.Background(), login.Session.AccessToken); kratoserrors.Reason(err) != "ACCESS_TOKEN_INVALID" {
		t.Fatalf("revoked token error = %v", err)
	}
	if _, err := uc.AuthenticateAccessToken(context.Background(), "unknown"); kratoserrors.Reason(err) != "ACCESS_TOKEN_INVALID" {
		t.Fatalf("unknown token error = %v", err)
	}
}

func TestProfileAndWorkoutPlanStayInUserDomain(t *testing.T) {
	uc := biz.NewUserUsecase(fakeWechatProvider{}, fakeUToolsProvider{}, fakePasswordHasher{}, newFakeUserRepo(), time.Hour, 24*time.Hour)
	login, err := uc.WechatLogin(context.Background(), "code", "device")
	if err != nil {
		t.Fatal(err)
	}
	profile := &biz.FitnessProfile{UserID: login.User.ID, Goal: "增肌", DaysPerWeek: 3, OnboardingCompleted: true}
	if err := uc.UpdateProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	plan, err := uc.GeneratePlan(context.Background(), login.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Goal != "增肌" || plan.UserID != login.User.ID {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestRegisterThenLoginWithPassword(t *testing.T) {
	repo := newFakeUserRepo()
	uc := biz.NewUserUsecase(fakeWechatProvider{}, fakeUToolsProvider{}, fakePasswordHasher{}, repo, time.Hour, 24*time.Hour)

	registered, err := uc.Register(context.Background(), " Web.User@Example.COM ", "password-123", "测试用户", "browser-1")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if registered.User.Nickname != "测试用户" || !registered.IsNewUser || !registered.OnboardingRequired {
		t.Fatalf("Register() = %#v", registered)
	}
	loggedIn, err := uc.Login(context.Background(), "web.user@example.com", "password-123", "browser-2")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if loggedIn.User.ID != registered.User.ID || loggedIn.IsNewUser {
		t.Fatalf("Login() = %#v", loggedIn)
	}
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	uc := biz.NewUserUsecase(fakeWechatProvider{}, fakeUToolsProvider{}, fakePasswordHasher{}, newFakeUserRepo(), time.Hour, 24*time.Hour)
	if _, err := uc.Register(context.Background(), "member-1@example.com", "password-123", "", ""); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	_, err := uc.Register(context.Background(), "MEMBER-1@EXAMPLE.COM", "password-456", "", "")
	if kratoserrors.Reason(err) != "EMAIL_ALREADY_EXISTS" {
		t.Fatalf("second Register() error = %v", err)
	}
}

func TestLoginDoesNotRevealWhetherEmailExists(t *testing.T) {
	uc := biz.NewUserUsecase(fakeWechatProvider{}, fakeUToolsProvider{}, fakePasswordHasher{}, newFakeUserRepo(), time.Hour, 24*time.Hour)
	if _, err := uc.Register(context.Background(), "member-1@example.com", "password-123", "", ""); err != nil {
		t.Fatal(err)
	}

	_, unknownUserErr := uc.Login(context.Background(), "unknown@example.com", "password-123", "")
	_, wrongPasswordErr := uc.Login(context.Background(), "member-1@example.com", "wrong-password", "")
	if kratoserrors.Reason(unknownUserErr) != "LOGIN_INVALID" || kratoserrors.Reason(wrongPasswordErr) != "LOGIN_INVALID" {
		t.Fatalf("login errors = (%v, %v)", unknownUserErr, wrongPasswordErr)
	}
}

func TestRegisterNormalizesInternationalEmailDomain(t *testing.T) {
	uc := biz.NewUserUsecase(fakeWechatProvider{}, fakeUToolsProvider{}, fakePasswordHasher{}, newFakeUserRepo(), time.Hour, 24*time.Hour)
	registered, err := uc.Register(context.Background(), " Member+Tag@例子.中国 ", "password-123", "", "")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if registered.User.Nickname != "member+tag" {
		t.Fatalf("Register() nickname = %q", registered.User.Nickname)
	}
	if _, err := uc.Login(context.Background(), "member+tag@xn--fsqu00a.xn--fiqs8s", "password-123", ""); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
}

func TestRegisterRejectsInvalidEmail(t *testing.T) {
	tests := []struct {
		name   string
		email  string
		reason string
	}{
		{name: "empty", email: "", reason: "EMAIL_REQUIRED"},
		{name: "missing at", email: "member.example.com", reason: "EMAIL_INVALID"},
		{name: "missing public suffix", email: "member@localhost", reason: "EMAIL_INVALID"},
		{name: "double dot", email: "member..one@example.com", reason: "EMAIL_INVALID"},
		{name: "display name", email: "Member <member@example.com>", reason: "EMAIL_INVALID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := biz.NewUserUsecase(fakeWechatProvider{}, fakeUToolsProvider{}, fakePasswordHasher{}, newFakeUserRepo(), time.Hour, 24*time.Hour)
			_, err := uc.Register(context.Background(), tt.email, "password-123", "", "")
			if kratoserrors.Reason(err) != tt.reason {
				t.Fatalf("Register() error = %v", err)
			}
		})
	}
}

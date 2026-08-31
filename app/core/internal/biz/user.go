package biz

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	stderrors "errors"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
	"golang.org/x/net/idna"
)

type WechatIdentity struct {
	AppID      string
	OpenID     string
	UnionID    string
	SessionKey string
}

// UToolsIdentity is the verified, plugin-scoped identity returned by uTools.
type UToolsIdentity struct {
	PluginID  string
	OpenID    string
	Nickname  string
	AvatarURI string
	Member    bool
}

// UserStatus 表示用户账号生命周期状态。
type UserStatus uint8

const (
	UserStatusUnspecified UserStatus = iota
	UserStatusActive
	UserStatusDisabled
)

// String 返回用户状态的持久化值。
func (s UserStatus) String() string {
	switch s {
	case UserStatusActive:
		return "active"
	case UserStatusDisabled:
		return "disabled"
	default:
		return ""
	}
}

// ParseUserStatus 校验并解析持久化用户状态。
func ParseUserStatus(raw string) (UserStatus, error) {
	switch raw {
	case UserStatusActive.String():
		return UserStatusActive, nil
	case UserStatusDisabled.String():
		return UserStatusDisabled, nil
	default:
		return UserStatusUnspecified, stderrors.New("unknown user status")
	}
}

type User struct {
	ID        string
	Nickname  string
	AvatarURI string
	Status    UserStatus
}

type PasswordUser struct {
	User         *User
	PasswordHash string
}

type FitnessProfile struct {
	UserID                  string
	Goal                    string
	ExperienceLevel         string
	DaysPerWeek             int32
	DurationMinutes         int32
	AvailableEquipmentCodes []string
	InjuryNotes             []string
	OnboardingCompleted     bool
}

type WorkoutPlan struct {
	ID     string
	UserID string
	Goal   string
	Status string
}

type Session struct {
	UserID        string
	DeviceID      string
	AccessToken   string
	RefreshToken  string
	AccessExpiry  time.Time
	RefreshExpiry time.Time
}

// AccessSession is the server-side state used to authenticate an access token.
type AccessSession struct {
	User         *User
	AccessExpiry time.Time
	Revoked      bool
}

type LoginResult struct {
	User               *User
	Session            *Session
	IsNewUser          bool
	OnboardingRequired bool
}

type WechatProvider interface {
	ExchangeCode(context.Context, string) (*WechatIdentity, error)
}

type UToolsIdentityProvider interface {
	VerifyTemporaryToken(context.Context, string) (*UToolsIdentity, error)
}

type PasswordHasher interface {
	Hash(string) (string, error)
	Verify(string, string) (bool, error)
}

var (
	ErrEmailAlreadyExists   = stderrors.New("email already exists")
	ErrPasswordUserNotFound = stderrors.New("password user not found")
	ErrAccessTokenNotFound  = stderrors.New("access token not found")
)

type UserRepo interface {
	UpsertWechatUser(context.Context, *WechatIdentity) (*User, bool, error)
	UpsertUToolsUser(context.Context, *UToolsIdentity) (*User, bool, error)
	CreatePasswordUser(context.Context, string, string, string) (*User, error)
	GetPasswordUser(context.Context, string) (*PasswordUser, error)
	SaveSession(context.Context, *Session) error
	GetSessionByRefreshToken(context.Context, string) (*Session, error)
	GetAccessSession(context.Context, string) (*AccessSession, error)
	DeleteSession(context.Context, string) error
	GetProfile(context.Context, string) (*FitnessProfile, error)
	SaveProfile(context.Context, *FitnessProfile) error
	SavePlan(context.Context, *WorkoutPlan) error
	GetPlan(context.Context, string, string) (*WorkoutPlan, error)
	CheckIn(context.Context, string, time.Time) (int32, error)
}

type UserUsecase struct {
	wechat         WechatProvider
	utools         UToolsIdentityProvider
	passwordHasher PasswordHasher
	repo           UserRepo
	accessTTL      time.Duration
	refreshTTL     time.Duration
}

// NewUserUsecase 创建用户用例并设置令牌有效期。
func NewUserUsecase(
	wechat WechatProvider,
	utools UToolsIdentityProvider,
	passwordHasher PasswordHasher,
	repo UserRepo,
	accessTTL time.Duration,
	refreshTTL time.Duration,
) *UserUsecase {
	if accessTTL <= 0 {
		accessTTL = 2 * time.Hour
	}
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * time.Hour
	}
	return &UserUsecase{
		wechat: wechat, utools: utools, passwordHasher: passwordHasher, repo: repo,
		accessTTL: accessTTL, refreshTTL: refreshTTL,
	}
}

// UToolsLogin verifies a one-time uTools token, links the plugin-scoped
// identity, and reuses the existing project session issuer.
func (uc *UserUsecase) UToolsLogin(ctx context.Context, temporaryToken, deviceID string) (*LoginResult, error) {
	temporaryToken = strings.TrimSpace(temporaryToken)
	if temporaryToken == "" {
		return nil, errors.BadRequest("UTOOLS_TEMPORARY_TOKEN_REQUIRED", "uTools temporary token is required")
	}
	if len(temporaryToken) != 32 {
		return nil, errors.BadRequest("UTOOLS_TEMPORARY_TOKEN_INVALID", "uTools temporary token must contain 32 bytes")
	}
	if uc.utools == nil {
		return nil, errors.ServiceUnavailable("UTOOLS_AUTH_NOT_CONFIGURED", "uTools authentication is not configured")
	}
	identity, err := uc.utools.VerifyTemporaryToken(ctx, temporaryToken)
	if err != nil {
		return nil, err
	}
	if err := validateUToolsIdentity(identity); err != nil {
		return nil, err
	}
	user, isNew, err := uc.repo.UpsertUToolsUser(ctx, identity)
	if err != nil {
		return nil, err
	}
	return uc.issueLogin(ctx, user, deviceID, isNew)
}

// Register 创建密码账号并签发首次登录会话。
func (uc *UserUsecase) Register(ctx context.Context, email, password, nickname, deviceID string) (*LoginResult, error) {
	if uc.passwordHasher == nil {
		return nil, errors.InternalServer("PASSWORD_AUTH_NOT_CONFIGURED", "password authentication is not configured")
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return nil, err
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		nickname = emailLocalPart(email)
	}
	if utf8.RuneCountInString(nickname) > 80 {
		return nil, errors.BadRequest("NICKNAME_TOO_LONG", "nickname must not exceed 80 characters")
	}
	passwordHash, err := uc.passwordHasher.Hash(password)
	if err != nil {
		return nil, errors.InternalServer("PASSWORD_HASH_FAILED", "failed to secure password").WithCause(err)
	}
	user, err := uc.repo.CreatePasswordUser(ctx, email, passwordHash, nickname)
	if stderrors.Is(err, ErrEmailAlreadyExists) {
		return nil, errors.Conflict("EMAIL_ALREADY_EXISTS", "email already exists")
	}
	if err != nil {
		return nil, err
	}
	return uc.issueLogin(ctx, user, deviceID, true)
}

// Login 校验 Web 邮箱和密码并签发会话。
func (uc *UserUsecase) Login(ctx context.Context, email, password, deviceID string) (*LoginResult, error) {
	if uc.passwordHasher == nil {
		return nil, errors.InternalServer("PASSWORD_AUTH_NOT_CONFIGURED", "password authentication is not configured")
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(password) == "" {
		return nil, errors.BadRequest("PASSWORD_REQUIRED", "password is required")
	}
	if len([]byte(password)) > 72 {
		return nil, errors.BadRequest("PASSWORD_TOO_LONG", "password must not exceed 72 bytes")
	}
	passwordUser, err := uc.repo.GetPasswordUser(ctx, email)
	if stderrors.Is(err, ErrPasswordUserNotFound) {
		return nil, invalidLoginError()
	}
	if err != nil {
		return nil, err
	}
	if passwordUser == nil || passwordUser.User == nil {
		return nil, errors.InternalServer("PASSWORD_USER_INVALID", "password account data is invalid")
	}
	matched, err := uc.passwordHasher.Verify(passwordUser.PasswordHash, password)
	if err != nil {
		return nil, errors.InternalServer("PASSWORD_VERIFY_FAILED", "failed to verify password").WithCause(err)
	}
	if !matched {
		return nil, invalidLoginError()
	}
	if passwordUser.User.Status != UserStatusActive {
		return nil, errors.Forbidden("USER_DISABLED", "user account is disabled")
	}
	return uc.issueLogin(ctx, passwordUser.User, deviceID, false)
}

// WechatLogin 完成微信 code 换取身份、自动注册和会话签发。
func (uc *UserUsecase) WechatLogin(ctx context.Context, code, deviceID string) (*LoginResult, error) {
	if strings.TrimSpace(code) == "" {
		return nil, errors.BadRequest("WECHAT_CODE_REQUIRED", "wechat login code is required")
	}
	if uc.wechat == nil {
		return nil, errors.InternalServer("WECHAT_AUTH_NOT_CONFIGURED", "wechat authentication is not configured")
	}
	identity, err := uc.wechat.ExchangeCode(ctx, code)
	if err != nil {
		return nil, err
	}
	user, isNew, err := uc.repo.UpsertWechatUser(ctx, identity)
	if err != nil {
		return nil, err
	}
	return uc.issueLogin(ctx, user, deviceID, isNew)
}

func (uc *UserUsecase) issueLogin(ctx context.Context, user *User, deviceID string, isNew bool) (*LoginResult, error) {
	if user == nil || strings.TrimSpace(user.ID) == "" {
		return nil, errors.InternalServer("LOGIN_USER_INVALID", "login user data is invalid")
	}
	if user.Status != UserStatusActive {
		return nil, errors.Forbidden("USER_DISABLED", "user account is disabled")
	}
	deviceID = strings.TrimSpace(deviceID)
	if err := validateDeviceID(deviceID); err != nil {
		return nil, err
	}
	session, err := uc.newSession(user.ID, deviceID)
	if err != nil {
		return nil, err
	}
	if err := uc.repo.SaveSession(ctx, session); err != nil {
		return nil, err
	}
	profile, err := uc.repo.GetProfile(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		User: user, Session: session, IsNewUser: isNew,
		OnboardingRequired: !profile.OnboardingCompleted,
	}, nil
}

// AuthenticateAccessToken resolves a hashed session and enforces revocation,
// expiry, and user lifecycle state before transport code injects current user.
func (uc *UserUsecase) AuthenticateAccessToken(ctx context.Context, accessToken string) (*User, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" || len(accessToken) > 256 {
		return nil, errors.Unauthorized("ACCESS_TOKEN_INVALID", "access token is invalid")
	}
	session, err := uc.repo.GetAccessSession(ctx, accessToken)
	if stderrors.Is(err, ErrAccessTokenNotFound) {
		return nil, errors.Unauthorized("ACCESS_TOKEN_INVALID", "access token is invalid")
	}
	if err != nil {
		return nil, err
	}
	if session == nil || session.User == nil {
		return nil, errors.InternalServer("ACCESS_SESSION_INVALID", "access session data is invalid")
	}
	if session.Revoked {
		return nil, errors.Unauthorized("ACCESS_TOKEN_INVALID", "access token is invalid")
	}
	if !time.Now().Before(session.AccessExpiry) {
		return nil, errors.Unauthorized("ACCESS_TOKEN_EXPIRED", "access token expired")
	}
	if session.User.Status != UserStatusActive {
		return nil, errors.Forbidden("USER_DISABLED", "user account is disabled")
	}
	return session.User, nil
}

// RefreshToken 校验并轮换刷新令牌。
func (uc *UserUsecase) RefreshToken(ctx context.Context, refreshToken string) (*Session, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, errors.BadRequest("REFRESH_TOKEN_REQUIRED", "refresh_token is required")
	}
	old, err := uc.repo.GetSessionByRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	if time.Now().After(old.RefreshExpiry) {
		_ = uc.repo.DeleteSession(ctx, refreshToken)
		return nil, errors.Unauthorized("REFRESH_TOKEN_EXPIRED", "refresh token expired")
	}
	next, err := uc.newSession(old.UserID, old.DeviceID)
	if err != nil {
		return nil, err
	}
	if err := uc.repo.DeleteSession(ctx, refreshToken); err != nil {
		return nil, err
	}
	if err := uc.repo.SaveSession(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
}

// Logout 撤销刷新令牌对应的登录会话。
func (uc *UserUsecase) Logout(ctx context.Context, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return errors.BadRequest("REFRESH_TOKEN_REQUIRED", "refresh_token is required")
	}
	return uc.repo.DeleteSession(ctx, refreshToken)
}

// GetProfile 查询用户健身档案。
func (uc *UserUsecase) GetProfile(ctx context.Context, userID string) (*FitnessProfile, error) {
	if err := validateUUID("user_id", userID); err != nil {
		return nil, err
	}
	return uc.repo.GetProfile(ctx, userID)
}

// UpdateProfile 校验并保存用户健身档案。
func (uc *UserUsecase) UpdateProfile(ctx context.Context, profile *FitnessProfile) error {
	if profile == nil || strings.TrimSpace(profile.UserID) == "" {
		return errors.BadRequest("USER_ID_REQUIRED", "user_id is required")
	}
	if err := validateUUID("user_id", profile.UserID); err != nil {
		return err
	}
	if profile.DaysPerWeek < 0 || profile.DaysPerWeek > 7 {
		return errors.BadRequest("DAYS_PER_WEEK_INVALID", "days_per_week must be between 0 and 7")
	}
	if profile.DurationMinutes < 0 {
		return errors.BadRequest("DURATION_MINUTES_INVALID", "duration_minutes must not be negative")
	}
	return uc.repo.SaveProfile(ctx, profile)
}

// GeneratePlan 根据已完成的健身档案生成训练计划。
func (uc *UserUsecase) GeneratePlan(ctx context.Context, userID string) (*WorkoutPlan, error) {
	if err := validateUUID("user_id", userID); err != nil {
		return nil, err
	}
	profile, err := uc.repo.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !profile.OnboardingCompleted || profile.Goal == "" {
		return nil, errors.BadRequest("FITNESS_PROFILE_INCOMPLETE", "complete fitness profile before generating a plan")
	}
	plan := &WorkoutPlan{ID: uuid.NewString(), UserID: userID, Goal: profile.Goal, Status: "draft"}
	if err := uc.repo.SavePlan(ctx, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// GetPlan 查询属于指定用户的训练计划。
func (uc *UserUsecase) GetPlan(ctx context.Context, userID, planID string) (*WorkoutPlan, error) {
	if err := validateUUID("user_id", userID); err != nil {
		return nil, err
	}
	if err := validateUUID("plan_id", planID); err != nil {
		return nil, err
	}
	return uc.repo.GetPlan(ctx, userID, planID)
}

// CheckIn 记录当天打卡并返回连续打卡天数。
func (uc *UserUsecase) CheckIn(ctx context.Context, userID string) (int32, error) {
	if strings.TrimSpace(userID) == "" {
		return 0, errors.BadRequest("USER_ID_REQUIRED", "user_id is required")
	}
	if err := validateUUID("user_id", userID); err != nil {
		return 0, err
	}
	return uc.repo.CheckIn(ctx, userID, time.Now())
}

func validateUToolsIdentity(identity *UToolsIdentity) error {
	if identity == nil {
		return errors.ServiceUnavailable("UTOOLS_INVALID_RESPONSE", "uTools returned invalid identity data")
	}
	identity.PluginID = strings.TrimSpace(identity.PluginID)
	identity.OpenID = strings.TrimSpace(identity.OpenID)
	identity.Nickname = strings.TrimSpace(identity.Nickname)
	identity.AvatarURI = strings.TrimSpace(identity.AvatarURI)
	if identity.PluginID == "" || len(identity.PluginID) > 64 || identity.OpenID == "" || len(identity.OpenID) > 128 {
		return errors.ServiceUnavailable("UTOOLS_INVALID_RESPONSE", "uTools returned invalid identity data")
	}
	if utf8.RuneCountInString(identity.Nickname) > 80 {
		return errors.ServiceUnavailable("UTOOLS_INVALID_RESPONSE", "uTools returned invalid profile data")
	}
	if identity.AvatarURI != "" {
		if len(identity.AvatarURI) > 2048 {
			return errors.ServiceUnavailable("UTOOLS_INVALID_RESPONSE", "uTools returned invalid profile data")
		}
		avatar, err := url.Parse(identity.AvatarURI)
		if err != nil || avatar.Scheme != "https" || avatar.Hostname() == "" || avatar.User != nil {
			return errors.ServiceUnavailable("UTOOLS_INVALID_RESPONSE", "uTools returned invalid profile data")
		}
	}
	return nil
}

func validateDeviceID(deviceID string) error {
	if len(deviceID) > 128 {
		return errors.BadRequest("DEVICE_ID_INVALID", "device_id must not exceed 128 bytes")
	}
	for _, char := range []byte(deviceID) {
		if isLowerASCIIAlphaNumeric(char) || char >= 'A' && char <= 'Z' || strings.ContainsRune("._:-", rune(char)) {
			continue
		}
		return errors.BadRequest("DEVICE_ID_INVALID", "device_id contains unsupported characters")
	}
	return nil
}

func validateUUID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.BadRequest(strings.ToUpper(field)+"_REQUIRED", field+" is required")
	}
	if _, err := uuid.Parse(value); err != nil {
		return errors.BadRequest(strings.ToUpper(field)+"_INVALID", field+" must be a valid UUID")
	}
	return nil
}

func normalizeEmail(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.BadRequest("EMAIL_REQUIRED", "email is required")
	}
	at := strings.LastIndexByte(raw, '@')
	if at <= 0 || at == len(raw)-1 || strings.Count(raw, "@") != 1 {
		return "", errors.BadRequest("EMAIL_INVALID", "email format is invalid")
	}
	local := strings.ToLower(raw[:at])
	if !validEmailLocalPart(local) {
		return "", errors.BadRequest("EMAIL_INVALID", "email format is invalid")
	}
	domain, err := idna.Lookup.ToASCII(raw[at+1:])
	if err != nil {
		return "", errors.BadRequest("EMAIL_INVALID", "email format is invalid")
	}
	domain = strings.ToLower(domain)
	if !validEmailDomain(domain) {
		return "", errors.BadRequest("EMAIL_INVALID", "email format is invalid")
	}
	email := local + "@" + domain
	if len(email) > 254 {
		return "", errors.BadRequest("EMAIL_TOO_LONG", "email must not exceed 254 bytes")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", errors.BadRequest("EMAIL_INVALID", "email format is invalid")
	}
	return email, nil
}

func validEmailLocalPart(local string) bool {
	if local == "" || len(local) > 64 || local[0] == '.' || local[len(local)-1] == '.' || strings.Contains(local, "..") {
		return false
	}
	const allowedSpecial = "!#$%&'*+-/=?^_`{|}~."
	for _, char := range []byte(local) {
		if isLowerASCIIAlphaNumeric(char) || strings.ContainsRune(allowedSpecial, rune(char)) {
			continue
		}
		return false
	}
	return true
}

func validEmailDomain(domain string) bool {
	if domain == "" || len(domain) > 253 || !strings.Contains(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range []byte(label) {
			if isLowerASCIIAlphaNumeric(char) || char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func isLowerASCIIAlphaNumeric(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
}

func emailLocalPart(email string) string {
	if at := strings.LastIndexByte(email, '@'); at > 0 {
		return email[:at]
	}
	return email
}

func validatePassword(password string) error {
	length := len([]byte(password))
	if length < 8 {
		return errors.BadRequest("PASSWORD_TOO_SHORT", "password must contain at least 8 bytes")
	}
	if length > 72 {
		return errors.BadRequest("PASSWORD_TOO_LONG", "password must not exceed 72 bytes")
	}
	return nil
}

func invalidLoginError() error {
	return errors.Unauthorized("LOGIN_INVALID", "email or password is invalid")
}

func (uc *UserUsecase) newSession(userID, deviceID string) (*Session, error) {
	accessToken, err := randomToken()
	if err != nil {
		return nil, errors.InternalServer("TOKEN_GENERATION_FAILED", err.Error())
	}
	refreshToken, err := randomToken()
	if err != nil {
		return nil, errors.InternalServer("TOKEN_GENERATION_FAILED", err.Error())
	}
	now := time.Now()
	return &Session{
		UserID: userID, DeviceID: deviceID,
		AccessToken: accessToken, RefreshToken: refreshToken,
		AccessExpiry: now.Add(uc.accessTTL), RefreshExpiry: now.Add(uc.refreshTTL),
	}, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

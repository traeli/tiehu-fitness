package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data/model"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
		return nil, kratoserrors.ServiceUnavailable("WECHAT_NOT_CONFIGURED", "WECHAT_APP_ID and WECHAT_APP_SECRET are required")
	}
	params := url.Values{
		"appid":      {c.appID},
		"secret":     {c.appSecret},
		"js_code":    {code},
		"grant_type": {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.weixin.qq.com/sns/jscode2session?"+params.Encode(), nil)
	if err != nil {
		return nil, kratoserrors.InternalServer("WECHAT_REQUEST_FAILED", err.Error())
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, kratoserrors.ServiceUnavailable("WECHAT_UNAVAILABLE", err.Error())
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
		return nil, kratoserrors.ServiceUnavailable("WECHAT_INVALID_RESPONSE", err.Error())
	}
	if result.ErrCode != 0 || result.OpenID == "" {
		return nil, kratoserrors.Unauthorized("WECHAT_LOGIN_FAILED", result.ErrMsg)
	}
	return &biz.WechatIdentity{AppID: c.appID, OpenID: result.OpenID, UnionID: result.UnionID, SessionKey: result.SessionKey}, nil
}

type UserRepo struct{ db *gorm.DB }

var _ biz.UserRepo = (*UserRepo)(nil)

func NewUserRepo(db *gorm.DB) biz.UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) UpsertWechatUser(ctx context.Context, identity *biz.WechatIdentity) (*biz.User, bool, error) {
	user, err := r.findWechatUser(ctx, r.db, identity.AppID, identity.OpenID)
	if err == nil {
		return user, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, databaseError(err)
	}

	var savedUser *biz.User
	var isNew bool
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidate, err := createUserWithProfile(tx, "", "")
		if err != nil {
			return err
		}
		wechatIdentity := model.WechatIdentity{
			UserID: candidate.ID, AppID: identity.AppID, OpenID: identity.OpenID, UnionID: identity.UnionID,
		}
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "app_id"}, {Name: "open_id"}},
			DoNothing: true,
		}).Create(&wechatIdentity)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			savedUser, err = toBizUser(candidate)
			if err != nil {
				return err
			}
			isNew = true
			return nil
		}

		// Another request registered the same OpenID first. Remove the unused
		// candidate and return the user created by the winning transaction.
		if err := tx.Unscoped().Delete(candidate).Error; err != nil {
			return err
		}
		existing, err := r.findWechatUser(ctx, tx, identity.AppID, identity.OpenID)
		if err != nil {
			return err
		}
		savedUser = existing
		return nil
	})
	if err != nil {
		return nil, false, databaseError(err)
	}
	return savedUser, isNew, nil
}

func (r *UserRepo) UpsertUToolsUser(ctx context.Context, identity *biz.UToolsIdentity) (*biz.User, bool, error) {
	_, err := r.findUToolsUser(ctx, r.db, identity.PluginID, identity.OpenID)
	if err == nil {
		var updated *biz.User
		if updateErr := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			updated, err = r.updateUToolsUser(ctx, tx, identity)
			return err
		}); updateErr != nil {
			return nil, false, databaseError(updateErr)
		}
		return updated, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, databaseError(err)
	}

	var savedUser *biz.User
	var isNew bool
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidate, err := createUserWithProfile(tx, identity.Nickname, identity.AvatarURI)
		if err != nil {
			return err
		}
		row := model.UToolsIdentity{
			UserID: candidate.ID, PluginID: identity.PluginID, OpenID: identity.OpenID, Member: identity.Member,
		}
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "plugin_id"}, {Name: "open_id"}},
			DoNothing: true,
		}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			savedUser, err = toBizUser(candidate)
			if err != nil {
				return err
			}
			isNew = true
			return nil
		}

		if err := tx.Unscoped().Delete(candidate).Error; err != nil {
			return err
		}
		savedUser, err = r.updateUToolsUser(ctx, tx, identity)
		return err
	})
	if err != nil {
		return nil, false, databaseError(err)
	}
	return savedUser, isNew, nil
}

func (r *UserRepo) CreatePasswordUser(ctx context.Context, email, passwordHash, nickname string) (*biz.User, error) {
	var savedUser *biz.User
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidate := model.User{Nickname: nickname, Status: biz.UserStatusActive.String()}
		if err := tx.Create(&candidate).Error; err != nil {
			return err
		}
		profile := model.FitnessProfile{
			UserID: candidate.ID, AvailableEquipmentCodes: emptyJSONArray(), InjuryNotes: emptyJSONArray(),
		}
		if err := tx.Create(&profile).Error; err != nil {
			return err
		}
		credential := model.PasswordCredential{
			UserID: candidate.ID, Email: email, PasswordHash: passwordHash,
		}
		if err := tx.Create(&credential).Error; err != nil {
			return err
		}
		var err error
		savedUser, err = toBizUser(&candidate)
		return err
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return nil, biz.ErrEmailAlreadyExists
	}
	if err != nil {
		return nil, databaseError(err)
	}
	return savedUser, nil
}

func (r *UserRepo) GetPasswordUser(ctx context.Context, email string) (*biz.PasswordUser, error) {
	type passwordUserRow struct {
		ID           string
		Nickname     string
		AvatarURI    string `gorm:"column:avatar_uri"`
		Status       string
		PasswordHash string `gorm:"column:password_hash"`
	}

	var row passwordUserRow
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Select("users.id, users.nickname, users.avatar_uri, users.status, pc.password_hash").
		Joins("JOIN password_credentials pc ON pc.user_id = users.id").
		Where("pc.email = ?", email).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrPasswordUserNotFound
	}
	if err != nil {
		return nil, databaseError(err)
	}
	user, err := toBizUser(&model.User{
		ID: row.ID, Nickname: row.Nickname, AvatarURI: row.AvatarURI, Status: row.Status,
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return &biz.PasswordUser{User: user, PasswordHash: row.PasswordHash}, nil
}

func (r *UserRepo) SaveSession(ctx context.Context, session *biz.Session) error {
	row := model.UserSession{
		UserID: session.UserID, DeviceID: session.DeviceID,
		AccessTokenHash: hashToken(session.AccessToken), RefreshTokenHash: hashToken(session.RefreshToken),
		AccessExpiresAt: session.AccessExpiry, RefreshExpiresAt: session.RefreshExpiry,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrForeignKeyViolated) {
			return kratoserrors.NotFound("USER_NOT_FOUND", "user not found")
		}
		return databaseError(err)
	}
	return nil
}

func (r *UserRepo) GetSessionByRefreshToken(ctx context.Context, token string) (*biz.Session, error) {
	var row model.UserSession
	err := r.db.WithContext(ctx).
		Where("refresh_token_hash = ? AND revoked_at IS NULL", hashToken(token)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kratoserrors.Unauthorized("REFRESH_TOKEN_INVALID", "refresh token is invalid")
	}
	if err != nil {
		return nil, databaseError(err)
	}
	return &biz.Session{
		UserID: row.UserID, DeviceID: row.DeviceID, RefreshToken: token,
		AccessExpiry: row.AccessExpiresAt, RefreshExpiry: row.RefreshExpiresAt,
	}, nil
}

func (r *UserRepo) GetAccessSession(ctx context.Context, token string) (*biz.AccessSession, error) {
	type accessSessionRow struct {
		UserID          string     `gorm:"column:user_id"`
		Nickname        string     `gorm:"column:nickname"`
		AvatarURI       string     `gorm:"column:avatar_uri"`
		UserStatus      string     `gorm:"column:user_status"`
		AccessExpiresAt time.Time  `gorm:"column:access_expires_at"`
		RevokedAt       *time.Time `gorm:"column:revoked_at"`
	}
	var row accessSessionRow
	err := r.db.WithContext(ctx).
		Table("user_sessions AS session").
		Select("session.user_id, users.nickname, users.avatar_uri, users.status AS user_status, session.access_expires_at, session.revoked_at").
		Joins("JOIN users ON users.id = session.user_id AND users.deleted_at IS NULL").
		Where("session.access_token_hash = ?", hashToken(token)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrAccessTokenNotFound
	}
	if err != nil {
		return nil, databaseError(err)
	}
	user, err := toBizUser(&model.User{
		ID: row.UserID, Nickname: row.Nickname, AvatarURI: row.AvatarURI, Status: row.UserStatus,
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return &biz.AccessSession{User: user, AccessExpiry: row.AccessExpiresAt, Revoked: row.RevokedAt != nil}, nil
}

func (r *UserRepo) DeleteSession(ctx context.Context, token string) error {
	now := time.Now()
	err := r.db.WithContext(ctx).Model(&model.UserSession{}).
		Where("refresh_token_hash = ? AND revoked_at IS NULL", hashToken(token)).
		Update("revoked_at", now).Error
	if err != nil {
		return databaseError(err)
	}
	return nil
}

func (r *UserRepo) GetProfile(ctx context.Context, userID string) (*biz.FitnessProfile, error) {
	var row model.FitnessProfile
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kratoserrors.NotFound("FITNESS_PROFILE_NOT_FOUND", "fitness profile not found")
	}
	if err != nil {
		return nil, databaseError(err)
	}
	return toBizFitnessProfile(&row)
}

func (r *UserRepo) SaveProfile(ctx context.Context, profile *biz.FitnessProfile) error {
	equipmentCodes, err := encodeStrings(profile.AvailableEquipmentCodes)
	if err != nil {
		return databaseError(err)
	}
	injuryNotes, err := encodeStrings(profile.InjuryNotes)
	if err != nil {
		return databaseError(err)
	}
	result := r.db.WithContext(ctx).Model(&model.FitnessProfile{}).
		Where("user_id = ?", profile.UserID).
		Updates(map[string]any{
			"goal": profile.Goal, "experience_level": profile.ExperienceLevel,
			"days_per_week": profile.DaysPerWeek, "duration_minutes": profile.DurationMinutes,
			"available_equipment_codes": equipmentCodes, "injury_notes": injuryNotes,
			"onboarding_completed": profile.OnboardingCompleted, "updated_at": time.Now(),
		})
	if result.Error != nil {
		return databaseError(result.Error)
	}
	if result.RowsAffected == 0 {
		return kratoserrors.NotFound("FITNESS_PROFILE_NOT_FOUND", "fitness profile not found")
	}
	return nil
}

func (r *UserRepo) SavePlan(ctx context.Context, plan *biz.WorkoutPlan) error {
	row := model.TrainingPlan{ID: plan.ID, UserID: plan.UserID, Goal: plan.Goal, Status: plan.Status}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrForeignKeyViolated) {
			return kratoserrors.NotFound("USER_NOT_FOUND", "user not found")
		}
		return databaseError(err)
	}
	return nil
}

func (r *UserRepo) GetPlan(ctx context.Context, userID, planID string) (*biz.WorkoutPlan, error) {
	var row model.TrainingPlan
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", planID, userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kratoserrors.NotFound("WORKOUT_PLAN_NOT_FOUND", "workout plan not found")
	}
	if err != nil {
		return nil, databaseError(err)
	}
	return &biz.WorkoutPlan{ID: row.ID, UserID: row.UserID, Goal: row.Goal, Status: row.Status}, nil
}

func (r *UserRepo) CheckIn(ctx context.Context, userID string, at time.Time) (int32, error) {
	checkDate := dateOnly(at)
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "check_date"}},
		DoNothing: true,
	}).Create(&model.CheckIn{UserID: userID, CheckDate: checkDate})
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrForeignKeyViolated) {
			return 0, kratoserrors.NotFound("USER_NOT_FOUND", "user not found")
		}
		return 0, databaseError(result.Error)
	}

	var dates []time.Time
	if err := r.db.WithContext(ctx).Model(&model.CheckIn{}).
		Where("user_id = ? AND check_date <= ?", userID, checkDate).
		Order("check_date DESC").Pluck("check_date", &dates).Error; err != nil {
		return 0, databaseError(err)
	}
	expected := checkDate
	var streak int32
	for _, item := range dates {
		if !sameDate(item, expected) {
			break
		}
		streak++
		expected = expected.AddDate(0, 0, -1)
	}
	return streak, nil
}

func (r *UserRepo) findWechatUser(ctx context.Context, db *gorm.DB, appID, openID string) (*biz.User, error) {
	var row model.User
	err := db.WithContext(ctx).Model(&model.User{}).
		Joins("JOIN wechat_identities wi ON wi.user_id = users.id").
		Where("wi.app_id = ? AND wi.open_id = ?", appID, openID).
		First(&row).Error
	if err != nil {
		return nil, err
	}
	return toBizUser(&row)
}

func (r *UserRepo) findUToolsUser(ctx context.Context, db *gorm.DB, pluginID, openID string) (*biz.User, error) {
	var row model.User
	err := db.WithContext(ctx).Model(&model.User{}).
		Joins("JOIN utools_identities ui ON ui.user_id = users.id").
		Where("ui.plugin_id = ? AND ui.open_id = ?", pluginID, openID).
		First(&row).Error
	if err != nil {
		return nil, err
	}
	return toBizUser(&row)
}

func (r *UserRepo) updateUToolsUser(ctx context.Context, db *gorm.DB, identity *biz.UToolsIdentity) (*biz.User, error) {
	user, err := r.findUToolsUser(ctx, db, identity.PluginID, identity.OpenID)
	if err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", user.ID).
		Updates(map[string]any{
			"nickname": identity.Nickname, "avatar_uri": identity.AvatarURI, "updated_at": time.Now(),
		}).Error; err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Model(&model.UToolsIdentity{}).
		Where("plugin_id = ? AND open_id = ?", identity.PluginID, identity.OpenID).
		Updates(map[string]any{"member": identity.Member, "updated_at": time.Now()}).Error; err != nil {
		return nil, err
	}
	user.Nickname = identity.Nickname
	user.AvatarURI = identity.AvatarURI
	return user, nil
}

func createUserWithProfile(db *gorm.DB, nickname, avatarURI string) (*model.User, error) {
	user := &model.User{Nickname: nickname, AvatarURI: avatarURI, Status: biz.UserStatusActive.String()}
	if err := db.Create(user).Error; err != nil {
		return nil, err
	}
	profile := model.FitnessProfile{
		UserID: user.ID, AvailableEquipmentCodes: emptyJSONArray(), InjuryNotes: emptyJSONArray(),
	}
	if err := db.Create(&profile).Error; err != nil {
		return nil, err
	}
	return user, nil
}

func toBizUser(row *model.User) (*biz.User, error) {
	status, err := biz.ParseUserStatus(row.Status)
	if err != nil {
		return nil, err
	}
	return &biz.User{ID: row.ID, Nickname: row.Nickname, AvatarURI: row.AvatarURI, Status: status}, nil
}

func toBizFitnessProfile(row *model.FitnessProfile) (*biz.FitnessProfile, error) {
	equipmentCodes, err := decodeStrings(row.AvailableEquipmentCodes)
	if err != nil {
		return nil, databaseError(err)
	}
	injuryNotes, err := decodeStrings(row.InjuryNotes)
	if err != nil {
		return nil, databaseError(err)
	}
	return &biz.FitnessProfile{
		UserID: row.UserID, Goal: row.Goal, ExperienceLevel: row.ExperienceLevel,
		DaysPerWeek: int32(row.DaysPerWeek), DurationMinutes: int32(row.DurationMinutes),
		AvailableEquipmentCodes: equipmentCodes, InjuryNotes: injuryNotes,
		OnboardingCompleted: row.OnboardingCompleted,
	}, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func dateOnly(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}

func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func emptyJSONArray() json.RawMessage { return json.RawMessage("[]") }

func encodeStrings(items []string) (json.RawMessage, error) {
	if items == nil {
		items = []string{}
	}
	data, err := json.Marshal(items)
	return json.RawMessage(data), err
}

func decodeStrings(data json.RawMessage) ([]string, error) {
	if len(data) == 0 {
		return []string{}, nil
	}
	var items []string
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	if items == nil {
		items = []string{}
	}
	return items, nil
}

func databaseError(err error) error {
	return kratoserrors.InternalServer("DATABASE_ERROR", "database operation failed").WithCause(err)
}

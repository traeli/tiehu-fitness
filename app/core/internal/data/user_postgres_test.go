package data

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUserRepoUToolsIdentityAndAccessSessionPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_CORE_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CORE_DATABASE_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewUserRepo(db)
	identity := &biz.UToolsIdentity{
		PluginID: "plugin-" + uuid.NewString(), OpenID: "open-" + uuid.NewString(),
		Nickname: "并发用户", AvatarURI: "https://res.u-tools.cn/avatar.png", Member: true,
	}

	const workers = 8
	users := make(chan *biz.User, workers)
	newFlags := make(chan bool, workers)
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			user, isNew, err := repo.UpsertUToolsUser(context.Background(), identity)
			if err != nil {
				errors <- err
				return
			}
			users <- user
			newFlags <- isNew
		}()
	}
	group.Wait()
	close(users)
	close(newFlags)
	close(errors)
	for err := range errors {
		t.Fatalf("UpsertUToolsUser() error = %v", err)
	}
	var userID string
	for user := range users {
		if user == nil || user.ID == "" {
			t.Fatal("UpsertUToolsUser() returned invalid user")
		}
		if userID == "" {
			userID = user.ID
		} else if user.ID != userID {
			t.Fatalf("concurrent user IDs differ: %s and %s", userID, user.ID)
		}
	}
	newCount := 0
	for isNew := range newFlags {
		if isNew {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatalf("new user count = %d, want 1", newCount)
	}
	t.Cleanup(func() {
		if err := db.WithContext(context.Background()).Exec("DELETE FROM users WHERE id = ?", userID).Error; err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})

	session := &biz.Session{
		UserID: userID, DeviceID: "device-1", AccessToken: "access-" + uuid.NewString(),
		RefreshToken: "refresh-" + uuid.NewString(), AccessExpiry: time.Now().Add(time.Hour),
		RefreshExpiry: time.Now().Add(24 * time.Hour),
	}
	if err := repo.SaveSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	access, err := repo.GetAccessSession(context.Background(), session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if access.User.ID != userID || access.Revoked || !access.AccessExpiry.Equal(session.AccessExpiry) {
		t.Fatalf("access session = %#v", access)
	}
}

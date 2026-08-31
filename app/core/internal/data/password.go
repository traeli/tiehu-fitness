package data

import (
	"errors"
	"fmt"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"golang.org/x/crypto/bcrypt"
)

type BcryptPasswordHasher struct {
	cost int
}

var _ biz.PasswordHasher = (*BcryptPasswordHasher)(nil)

func NewPasswordHasher() biz.PasswordHasher {
	return &BcryptPasswordHasher{cost: bcrypt.DefaultCost}
}

func (h *BcryptPasswordHasher) Hash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", fmt.Errorf("generate password hash: %w", err)
	}
	return string(hash), nil
}

func (h *BcryptPasswordHasher) Verify(encodedHash, password string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(encodedHash), []byte(password))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return false, nil
	}
	return false, fmt.Errorf("compare password hash: %w", err)
}

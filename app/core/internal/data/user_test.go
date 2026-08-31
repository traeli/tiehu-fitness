package data

import (
	"reflect"
	"testing"
	"time"
)

func TestHashTokenDoesNotStorePlaintext(t *testing.T) {
	const token = "refresh-token"
	hash := hashToken(token)
	if hash == token || len(hash) != 64 {
		t.Fatalf("hashToken() = %q", hash)
	}
	if hash != hashToken(token) {
		t.Fatal("hashToken() must be deterministic")
	}
}

func TestBcryptPasswordHasher(t *testing.T) {
	hasher := NewPasswordHasher()
	hash, err := hasher.Hash("password-123")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if hash == "password-123" {
		t.Fatal("Hash() must not return plaintext")
	}
	matched, err := hasher.Verify(hash, "password-123")
	if err != nil || !matched {
		t.Fatalf("Verify() = (%v, %v)", matched, err)
	}
	matched, err = hasher.Verify(hash, "wrong-password")
	if err != nil || matched {
		t.Fatalf("Verify() wrong password = (%v, %v)", matched, err)
	}
}

func TestStringJSONRoundTrip(t *testing.T) {
	want := []string{"背部", "肩部"}
	data, err := encodeStrings(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeStrings(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decodeStrings() = %#v, want %#v", got, want)
	}
}

func TestSameDateIgnoresTimezoneLocation(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	localDate := time.Date(2026, 8, 26, 0, 0, 0, 0, shanghai)
	utcDate := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	if !sameDate(localDate, utcDate) {
		t.Fatal("same calendar date in different locations should match")
	}
}

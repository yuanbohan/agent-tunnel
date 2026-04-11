package auth

import (
	"context"
	"strings"
	"testing"
)

func TestNormalizeUsername(t *testing.T) {
	got, err := NormalizeUsername("  Alice.Example_1 ")
	if err != nil {
		t.Fatalf("NormalizeUsername returned error: %v", err)
	}
	if got != "alice.example_1" {
		t.Fatalf("NormalizeUsername = %q, want %q", got, "alice.example_1")
	}
}

func TestNormalizeUsernameRejectsInvalidCharacters(t *testing.T) {
	if _, err := NormalizeUsername("bad name"); err == nil {
		t.Fatal("expected invalid username error")
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("12345678"); err != nil {
		t.Fatalf("ValidatePassword returned error: %v", err)
	}
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("expected short password error")
	}
}

func TestNormalizeInviteCode(t *testing.T) {
	got, err := NormalizeInviteCode("ab2c3d")
	if err != nil {
		t.Fatalf("NormalizeInviteCode returned error: %v", err)
	}
	if got != "AB2C3D" {
		t.Fatalf("NormalizeInviteCode = %q, want %q", got, "AB2C3D")
	}
}

func TestGenerateInviteCodeUsesAlphabet(t *testing.T) {
	code, err := GenerateInviteCode()
	if err != nil {
		t.Fatalf("GenerateInviteCode returned error: %v", err)
	}
	if len(code) != inviteCodeLength {
		t.Fatalf("len(code) = %d, want %d", len(code), inviteCodeLength)
	}
	for _, r := range code {
		if !strings.ContainsRune(inviteCodeAlphabet, r) {
			t.Fatalf("invite code rune %q not in alphabet %q", r, inviteCodeAlphabet)
		}
	}
}

func TestSecretDigesterIsStable(t *testing.T) {
	digester, err := NewSecretDigester("secret-key")
	if err != nil {
		t.Fatalf("NewSecretDigester returned error: %v", err)
	}
	a := digester.Digest("value")
	b := digester.Digest("value")
	if a != b {
		t.Fatalf("Digest mismatch: %q != %q", a, b)
	}
	if a == digester.Digest("other") {
		t.Fatal("expected distinct digests for distinct values")
	}
}

func TestPasswordHasherRoundTrip(t *testing.T) {
	hasher := PasswordHasher{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	}

	hash, err := hasher.HashPassword(context.Background(), "password123")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if err := hasher.VerifyPassword("password123", hash); err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if err := hasher.VerifyPassword("wrong-password", hash); err == nil {
		t.Fatal("expected wrong password to fail")
	}
}

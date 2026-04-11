package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	minUsernameLength = 4
	minPasswordLength = 8
	inviteCodeLength  = 6
)

const inviteCodeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

var (
	ErrInvalidUsername          = errors.New("invalid username")
	ErrInvalidPassword          = errors.New("invalid password")
	ErrInvalidInviteCode        = errors.New("invalid invite code")
	ErrInvalidApplicationSecret = errors.New("invalid application secret")
	ErrInvalidPasswordHash      = errors.New("invalid password hash")
)

type PasswordHasher struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

func DefaultPasswordHasher() PasswordHasher {
	return PasswordHasher{
		MemoryKiB:   64 * 1024,
		Iterations:  3,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	}
}

func NormalizeUsername(raw string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if len(normalized) < minUsernameLength {
		return "", ErrInvalidUsername
	}
	for _, r := range normalized {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return "", ErrInvalidUsername
		}
	}
	return normalized, nil
}

func ValidatePassword(raw string) error {
	if len(raw) < minPasswordLength {
		return ErrInvalidPassword
	}
	return nil
}

func NormalizeInviteCode(raw string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	if len(normalized) != inviteCodeLength {
		return "", ErrInvalidInviteCode
	}
	for _, r := range normalized {
		if !strings.ContainsRune(inviteCodeAlphabet, r) {
			return "", ErrInvalidInviteCode
		}
	}
	return normalized, nil
}

func GenerateInviteCode() (string, error) {
	code := make([]byte, inviteCodeLength)
	limit := 256 - (256 % len(inviteCodeAlphabet))
	buf := make([]byte, inviteCodeLength)
	for i := 0; i < len(code); {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			code[i] = inviteCodeAlphabet[int(b)%len(inviteCodeAlphabet)]
			i++
			if i == len(code) {
				break
			}
		}
	}
	return string(code), nil
}

func GenerateOpaqueToken(byteCount int) (string, error) {
	if byteCount <= 0 {
		byteCount = 32
	}
	buf := make([]byte, byteCount)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func GenerateOpaqueID(prefix string, byteCount int) (string, error) {
	token, err := GenerateOpaqueToken(byteCount)
	if err != nil {
		return "", err
	}
	if prefix == "" {
		return token, nil
	}
	return prefix + "_" + token, nil
}

func NewSecretDigester(secret string) (*SecretDigester, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrInvalidApplicationSecret
	}
	return &SecretDigester{key: []byte(secret)}, nil
}

type SecretDigester struct {
	key []byte
}

func (d *SecretDigester) Digest(raw string) string {
	return d.digestFunc(raw, sha256.New)
}

func (d *SecretDigester) DigestNormalizedInviteCode(raw string) (string, error) {
	normalized, err := NormalizeInviteCode(raw)
	if err != nil {
		return "", err
	}
	return d.Digest(normalized), nil
}

func (d *SecretDigester) DigestNormalizedUsername(raw string) (string, error) {
	normalized, err := NormalizeUsername(raw)
	if err != nil {
		return "", err
	}
	return d.Digest(normalized), nil
}

func (d *SecretDigester) digestFunc(raw string, newHash func() hash.Hash) string {
	mac := hmac.New(newHash, d.key)
	_, _ = mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h PasswordHasher) HashPassword(_ context.Context, raw string) (string, error) {
	if err := ValidatePassword(raw); err != nil {
		return "", err
	}
	salt := make([]byte, h.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(raw), salt, h.Iterations, h.MemoryKiB, h.Parallelism, h.KeyLength)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		h.MemoryKiB,
		h.Iterations,
		h.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func (h PasswordHasher) VerifyPassword(raw, encoded string) error {
	if err := ValidatePassword(raw); err != nil {
		return err
	}

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return ErrInvalidPasswordHash
	}

	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return ErrInvalidPasswordHash
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return ErrInvalidPasswordHash
	}
	if memory == 0 || iterations == 0 || parallelism == 0 {
		return ErrInvalidPasswordHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrInvalidPasswordHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrInvalidPasswordHash
	}
	if len(salt) == 0 || len(want) == 0 {
		return ErrInvalidPasswordHash
	}

	got := argon2.IDKey([]byte(raw), salt, iterations, memory, parallelism, uint32(len(want)))
	if !hmac.Equal(got, want) {
		return ErrInvalidPassword
	}
	return nil
}

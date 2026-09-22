package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidToken       = errors.New("invalid or expired auth token")
	ErrTokenRevoked       = errors.New("token has been revoked")
	ErrUserNotFound       = errors.New("superuser not found")
	ErrUserExists         = errors.New("superuser already exists")
)

// Superuser represents an administrative account modeled after PocketBase v0.25+.
type Superuser struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	TokenKey     string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Claims represents JWT token payload.
type Claims struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	TokenKey  string `json:"token_key"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
}

// Config provides configuration for AuthService.
type Config struct {
	SecretKey     []byte
	TokenDuration time.Duration
}

// AuthService manages superuser authentication, password verification, and JWT lifecycles.
type AuthService struct {
	secret        []byte
	tokenDuration time.Duration
	users         map[string]*Superuser // keyed by normalized email
	usersByID     map[string]*Superuser
	mu            sync.RWMutex
}

// NewAuthService creates a new AuthService.
func NewAuthService(cfg Config) *AuthService {
	secret := cfg.SecretKey
	if len(secret) == 0 {
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
	}
	dur := cfg.TokenDuration
	if dur <= 0 {
		dur = 14 * 24 * time.Hour // Default 14 days (matching PocketBase standard)
	}
	return &AuthService{
		secret:        secret,
		tokenDuration: dur,
		users:         make(map[string]*Superuser),
		usersByID:     make(map[string]*Superuser),
	}
}

// HashPassword generates a salted SHA256 password hash: salt:hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(password))
	hash := h.Sum(nil)
	return fmt.Sprintf("%s:%s", hex.EncodeToString(salt), hex.EncodeToString(hash)), nil
}

// VerifyPassword checks plaintext password against salted hash.
func VerifyPassword(password, storedHash string) bool {
	parts := strings.Split(storedHash, ":")
	if len(parts) != 2 {
		return false
	}
	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}
	expectedHash, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(password))
	actualHash := h.Sum(nil)

	return subtle.ConstantTimeCompare(expectedHash, actualHash) == 1
}

func generateRandomKey(byteLen int) string {
	b := make([]byte, byteLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// EnsureDefaultSuperuser creates or resets the default superuser if not already created.
func (s *AuthService) EnsureDefaultSuperuser(email, password string) (*Superuser, error) {
	normEmail := strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()

	if u, exists := s.users[normEmail]; exists {
		return u, nil
	}

	pwHash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	u := &Superuser{
		ID:           generateRandomKey(8),
		Email:        normEmail,
		PasswordHash: pwHash,
		TokenKey:     generateRandomKey(16),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	s.users[normEmail] = u
	s.usersByID[u.ID] = u
	return u, nil
}

// Authenticate checks email/password and returns the superuser plus a newly signed JWT.
func (s *AuthService) Authenticate(email, password string) (*Superuser, string, error) {
	normEmail := strings.ToLower(strings.TrimSpace(email))
	s.mu.RLock()
	u, exists := s.users[normEmail]
	s.mu.RUnlock()

	if !exists || !VerifyPassword(password, u.PasswordHash) {
		return nil, "", ErrInvalidCredentials
	}

	token, err := s.GenerateToken(u)
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}

// GenerateToken creates a signed HS256 JWT for the given superuser.
func (s *AuthService) GenerateToken(u *Superuser) (string, error) {
	s.mu.RLock()
	tokenKey := u.TokenKey
	s.mu.RUnlock()

	now := time.Now().UTC()
	claims := Claims{
		ID:        u.ID,
		Email:     u.Email,
		TokenKey:  tokenKey,
		ExpiresAt: now.Add(s.tokenDuration).Unix(),
		IssuedAt:  now.Unix(),
	}

	headerJSON, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	sigInput := headerB64 + "." + payloadB64

	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(sigInput))
	sig := mac.Sum(nil)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return sigInput + "." + sigB64, nil
}

// ValidateToken verifies JWT signature, expiration, and matches the live user TokenKey.
func (s *AuthService) ValidateToken(tokenStr string) (*Superuser, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	sigInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrInvalidToken
	}

	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(sigInput))
	expectedSig := mac.Sum(nil)

	if subtle.ConstantTimeCompare(sig, expectedSig) != 1 {
		return nil, ErrInvalidToken
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}

	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, ErrInvalidToken
	}

	if time.Now().UTC().Unix() >= claims.ExpiresAt {
		return nil, ErrInvalidToken
	}

	s.mu.RLock()
	u, exists := s.usersByID[claims.ID]
	s.mu.RUnlock()

	if !exists {
		return nil, ErrUserNotFound
	}

	// Verify token key (immediate revocation guard)
	s.mu.RLock()
	tokenKey := u.TokenKey
	s.mu.RUnlock()

	if tokenKey != claims.TokenKey {
		return nil, ErrTokenRevoked
	}

	return u, nil
}

// RevokeAllTokens rotates the user's TokenKey, immediately invalidating all active JWTs.
func (s *AuthService) RevokeAllTokens(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, exists := s.usersByID[userID]
	if !exists {
		return ErrUserNotFound
	}

	u.TokenKey = generateRandomKey(16)
	u.UpdatedAt = time.Now().UTC()
	return nil
}

// RefreshToken validates an existing valid token and issues a fresh one.
func (s *AuthService) RefreshToken(tokenStr string) (*Superuser, string, error) {
	u, err := s.ValidateToken(tokenStr)
	if err != nil {
		return nil, "", err
	}
	freshToken, err := s.GenerateToken(u)
	if err != nil {
		return nil, "", err
	}
	return u, freshToken, nil
}

// GetUserByID returns user by ID.
func (s *AuthService) GetUserByID(id string) (*Superuser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, exists := s.usersByID[id]
	if !exists {
		return nil, ErrUserNotFound
	}
	return u, nil
}

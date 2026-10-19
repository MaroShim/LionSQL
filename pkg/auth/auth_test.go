package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestAuth_HashAndVerifyPassword(t *testing.T) {
	raw := "secret12345"
	hash, err := HashPassword(raw)
	if err != nil {
		t.Fatalf("Failed to hash password: %v", err)
	}

	if !VerifyPassword(raw, hash) {
		t.Fatalf("Password verification failed for correct password")
	}

	if VerifyPassword("wrongpassword", hash) {
		t.Fatalf("Password verification succeeded for incorrect password")
	}
}

func TestAuth_TokenLifecycleAndRevocation(t *testing.T) {
	svc := NewAuthService(Config{
		TokenDuration: 2 * time.Second,
	})

	user, err := svc.EnsureDefaultSuperuser("admin@lionsql.local", "mypassword")
	if err != nil {
		t.Fatalf("EnsureDefaultSuperuser failed: %v", err)
	}

	// 1. Authenticate
	u, token, err := svc.Authenticate("admin@lionsql.local", "mypassword")
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if u.Email != "admin@lionsql.local" {
		t.Fatalf("Expected email admin@lionsql.local, got %s", u.Email)
	}

	// 2. Validate token
	validated, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if validated.ID != user.ID {
		t.Fatalf("Validated user ID mismatch")
	}

	// 3. Refresh token
	_, refreshedToken, err := svc.RefreshToken(token)
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}
	if refreshedToken == "" {
		t.Fatalf("Refreshed token empty")
	}

	// 4. Revocation via token_key rotation
	err = svc.RevokeAllTokens(user.ID)
	if err != nil {
		t.Fatalf("RevokeAllTokens failed: %v", err)
	}

	// Both old and refreshed tokens must now be rejected
	_, err = svc.ValidateToken(token)
	if err != ErrTokenRevoked {
		t.Fatalf("Expected ErrTokenRevoked for old token, got %v", err)
	}

	_, err = svc.ValidateToken(refreshedToken)
	if err != ErrTokenRevoked {
		t.Fatalf("Expected ErrTokenRevoked for refreshed token, got %v", err)
	}

	// New login must issue working token with new token_key
	_, newToken, err := svc.Authenticate("admin@lionsql.local", "mypassword")
	if err != nil {
		t.Fatalf("Authenticate after revocation failed: %v", err)
	}
	if _, err := svc.ValidateToken(newToken); err != nil {
		t.Fatalf("New token failed validation: %v", err)
	}
}

func TestAuth_TokenExpiration(t *testing.T) {
	svc := NewAuthService(Config{
		TokenDuration: 1 * time.Second,
	})

	_, err := svc.EnsureDefaultSuperuser("fast@lionsql.local", "password")
	if err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}

	_, token, err := svc.Authenticate("fast@lionsql.local", "password")
	if err != nil {
		t.Fatalf("Auth failed: %v", err)
	}

	parts := strings.Split(token, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	t.Logf("Token payload: %s, Current time: %d", string(payload), time.Now().UTC().Unix())

	time.Sleep(1200 * time.Millisecond)

	t.Logf("Time after sleep: %d", time.Now().UTC().Unix())
	_, err = svc.ValidateToken(token)
	if err != ErrInvalidToken {
		t.Fatalf("Expected ErrInvalidToken for expired token, got %v", err)
	}
}

func TestAuth_APIKeys(t *testing.T) {
	svc := NewAuthService(Config{})

	// 1. Create read-only key for accounts table
	keyObj, err := svc.CreateAPIKey("frontend-app", RoleReadOnly, []string{"accounts"})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}
	if !strings.HasPrefix(keyObj.Key, "sqlion_") {
		t.Fatalf("Expected sqlion_ prefix in key, got %s", keyObj.Key)
	}

	// 2. Validate API key
	validated, err := svc.ValidateAPIKey(keyObj.Key)
	if err != nil {
		t.Fatalf("ValidateAPIKey failed: %v", err)
	}
	if validated.ID != keyObj.ID {
		t.Fatalf("ID mismatch in validated key")
	}

	// 3. Test permissions
	if err := validated.CheckTableAccess("accounts", false); err != nil {
		t.Fatalf("Expected read access to accounts allowed, got: %v", err)
	}
	if err := validated.CheckTableAccess("accounts", true); err == nil {
		t.Fatalf("Expected write access to accounts rejected for read-only key")
	}
	if err := validated.CheckTableAccess("users", false); err == nil {
		t.Fatalf("Expected read access to unlisted table users rejected")
	}

	// 4. Create admin key with wildcard access
	adminKey, err := svc.CreateAPIKey("backend-service", RoleAdmin, nil)
	if err != nil {
		t.Fatalf("Create admin key failed: %v", err)
	}
	if err := adminKey.CheckTableAccess("anything", true); err != nil {
		t.Fatalf("Expected admin write access allowed to all tables, got: %v", err)
	}

	// 5. List keys
	keys := svc.ListAPIKeys()
	if len(keys) != 2 {
		t.Fatalf("Expected 2 keys in list, got %d", len(keys))
	}
	for _, k := range keys {
		if k.Key != "" {
			t.Fatalf("Listed key must not reveal plaintext secret")
		}
	}

	// 6. Revoke key
	err = svc.RevokeAPIKey(keyObj.ID)
	if err != nil {
		t.Fatalf("RevokeAPIKey failed: %v", err)
	}
	if _, err := svc.ValidateAPIKey(keyObj.Key); err != ErrInvalidAPIKey {
		t.Fatalf("Expected ErrInvalidAPIKey after revocation, got %v", err)
	}
}

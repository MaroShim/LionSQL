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

	user, err := svc.EnsureDefaultSuperuser("admin@sqlion.local", "mypassword")
	if err != nil {
		t.Fatalf("EnsureDefaultSuperuser failed: %v", err)
	}

	// 1. Authenticate
	u, token, err := svc.Authenticate("admin@sqlion.local", "mypassword")
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if u.Email != "admin@sqlion.local" {
		t.Fatalf("Expected email admin@sqlion.local, got %s", u.Email)
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
	_, newToken, err := svc.Authenticate("admin@sqlion.local", "mypassword")
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

	_, err := svc.EnsureDefaultSuperuser("fast@sqlion.local", "password")
	if err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}

	_, token, err := svc.Authenticate("fast@sqlion.local", "password")
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

func TestAuth_OAuthProvisioning(t *testing.T) {
	svc := NewAuthService(Config{})

	// 1. Provision new OAuth user
	u1, err := svc.ProvisionOAuthUser("google", "1001", "dev@example.com", "Dev User", "https://img.test/pic.png")
	if err != nil {
		t.Fatalf("ProvisionOAuthUser failed: %v", err)
	}
	if u1.OAuthProvider != "google" || u1.OAuthID != "1001" || u1.Email != "dev@example.com" {
		t.Fatalf("Unexpected user state: %+v", u1)
	}
	if u1.Name != "Dev User" || u1.AvatarURL != "https://img.test/pic.png" {
		t.Fatalf("Unexpected name/avatar: %+v", u1)
	}

	// 2. Fetch by OAuth
	found := svc.GetUserByOAuth("google", "1001")
	if found == nil || found.ID != u1.ID {
		t.Fatalf("GetUserByOAuth failed to find user")
	}

	// 3. Re-provision same OAuth user with updated profile
	u1Updated, err := svc.ProvisionOAuthUser("google", "1001", "dev@example.com", "Dev Updated", "https://img.test/pic2.png")
	if err != nil {
		t.Fatalf("Re-provision failed: %v", err)
	}
	if u1Updated.ID != u1.ID || u1Updated.Name != "Dev Updated" {
		t.Fatalf("Expected updated user with same ID, got %+v", u1Updated)
	}

	// 4. Link existing user by email
	existing, err := svc.EnsureDefaultSuperuser("github_user@example.com", "strongpass")
	if err != nil {
		t.Fatalf("EnsureDefaultSuperuser failed: %v", err)
	}
	linked, err := svc.ProvisionOAuthUser("github", "2002", "github_user@example.com", "GitHub User", "")
	if err != nil {
		t.Fatalf("Linking existing user via OAuth failed: %v", err)
	}
	if linked.ID != existing.ID {
		t.Fatalf("Expected linked user to have existing ID %s, got %s", existing.ID, linked.ID)
	}
	if linked.OAuthProvider != "github" || linked.OAuthID != "2002" {
		t.Fatalf("Expected github provider link, got %+v", linked)
	}

	// 5. Generate and validate token for OAuth provisioned user
	token, err := svc.GenerateToken(u1)
	if err != nil {
		t.Fatalf("GenerateToken failed for OAuth user: %v", err)
	}
	validated, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if validated.ID != u1.ID {
		t.Fatalf("Validated ID mismatch: %s vs %s", validated.ID, u1.ID)
	}
}

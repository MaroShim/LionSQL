package auth_test

import (
	"os"
	"testing"

	"lionsql/pkg/auth"
)

// TestRBAC_Regression_P0_09_MetadataPersistenceAcrossRestart verifies that custom logins,
// database users, and table grants survive service restarts via atomic auth.json persistence (P0-09).
func TestRBAC_Regression_P0_09_MetadataPersistenceAcrossRestart(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_auth_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// --- Phase 1: Initialize first AuthService and configure custom security principals ---
	svc1 := auth.NewAuthService(auth.Config{
		DataDir: tmpDir,
	})
	_, err = svc1.EnsureDefaultSuperuser("admin@lionsql.local", "SuperAdminPass123!")
	if err != nil {
		t.Fatalf("Phase 1: EnsureDefaultSuperuser failed: %v", err)
	}

	// 1. Create a custom login
	login, err := svc1.CreateLogin("alice", "AliceSecretPass123!")
	if err != nil {
		t.Fatalf("Phase 1: CreateLogin failed: %v", err)
	}
	if login.Name != "alice" {
		t.Fatalf("Phase 1: Expected login name 'alice', got %q", login.Name)
	}

	// 2. Create a database user bound to the login
	user, err := svc1.CreateUser("alice", "alice", "analyst")
	if err != nil {
		t.Fatalf("Phase 1: CreateUser failed: %v", err)
	}
	if user.Name != "alice" {
		t.Fatalf("Phase 1: Expected user name 'alice', got %q", user.Name)
	}

	// 3. Grant table permission
	if err := svc1.GrantTable("alice", "orders", auth.PrivSelect); err != nil {
		t.Fatalf("Phase 1: GrantTable failed: %v", err)
	}

	// 4. Create an API Key
	apiKey, err := svc1.CreateAPIKey("reporting-svc", auth.RoleReadOnly, []string{"orders"})
	if err != nil {
		t.Fatalf("Phase 1: CreateAPIKey failed: %v", err)
	}

	// Verify Phase 1 permissions in-memory
	ok, err := svc1.HasTablePermission("alice", "orders", auth.PrivSelect)
	if err != nil || !ok {
		t.Fatalf("Phase 1: Expected alice to have SELECT on orders, got allowed=%v, err=%v", ok, err)
	}

	// --- Phase 2: Simulate Server Restart (Instantiate brand new AuthService from same DataDir) ---
	svc2 := auth.NewAuthService(auth.Config{
		DataDir: tmpDir,
	})

	// 1. Verify alice login survived and password verification still works
	authenticatedLogin, err := svc2.AuthenticateLogin("alice", "AliceSecretPass123!")
	if err != nil {
		t.Fatalf("Phase 2: [CRITICAL P0-09 REGRESSION] Alice login evaporated after restart: %v", err)
	}
	if authenticatedLogin == nil || authenticatedLogin.Name != "alice" {
		t.Fatalf("Phase 2: Expected authenticated login for 'alice', got %v", authenticatedLogin)
	}

	// 2. Verify alice database user survived
	dbUser, err := svc2.GetUser("alice")
	if err != nil {
		t.Fatalf("Phase 2: [CRITICAL P0-09 REGRESSION] Alice DB user evaporated after restart: %v", err)
	}
	if dbUser == nil || dbUser.Name != "alice" {
		t.Fatalf("Phase 2: Expected DB user 'alice', got %v", dbUser)
	}

	// 3. Verify table grant survived
	allowed, err := svc2.HasTablePermission("alice", "orders", auth.PrivSelect)
	if err != nil {
		t.Fatalf("Phase 2: Error checking table permission: %v", err)
	}
	if !allowed {
		t.Fatalf("Phase 2: [CRITICAL P0-09 REGRESSION] Alice grant on 'orders' evaporated after restart")
	}

	// 4. Verify unauthorized table is still denied (Default Deny integrity)
	unauthAllowed, err := svc2.HasTablePermission("alice", "secret_finances", auth.PrivSelect)
	if err != nil {
		t.Fatalf("Phase 2: Error checking unauthorized table permission: %v", err)
	}
	if unauthAllowed {
		t.Fatalf("Phase 2: Expected secret_finances to be denied for alice")
	}

	// 5. Verify API key survived
	keyLookup, err := svc2.ValidateAPIKey(apiKey.Key)
	if err != nil {
		t.Fatalf("Phase 2: [CRITICAL P0-09 REGRESSION] API key validation failed after restart: %v", err)
	}
	if keyLookup == nil || keyLookup.Name != "reporting-svc" {
		t.Fatalf("Phase 2: Expected API key name 'reporting-svc', got %v", keyLookup)
	}
}

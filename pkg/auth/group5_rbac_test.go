package auth_test

import (
	"testing"

	"lionsql/pkg/auth"
)

func TestRBAC_CustomRolePermissions(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{})

	// 1. Setup login and user with custom role "analyst"
	if _, err := svc.CreateLogin("alice", "Pass1234!"); err != nil {
		t.Fatalf("CreateLogin failed: %v", err)
	}
	if _, err := svc.CreateUser("alice_user", "alice", "analyst"); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Without permission, HasTablePermission should be false
	allowed, err := svc.HasTablePermission("alice_user", "financials", auth.PrivSelect)
	if err != nil {
		t.Fatalf("HasTablePermission failed: %v", err)
	}
	if allowed {
		t.Fatalf("expected alice_user to not have SELECT on financials initially")
	}

	// 2. Grant SELECT to custom role "analyst"
	if err := svc.GrantTable("analyst", "financials", auth.PrivSelect); err != nil {
		t.Fatalf("GrantTable failed: %v", err)
	}

	// Now alice_user should have SELECT on financials via the role
	allowed, err = svc.HasTablePermission("alice_user", "financials", auth.PrivSelect)
	if err != nil {
		t.Fatalf("HasTablePermission failed: %v", err)
	}
	if !allowed {
		t.Fatalf("expected alice_user to have SELECT on financials via role 'analyst'")
	}

	// 3. Deny SELECT to custom role "analyst", verify DENY takes precedence
	if err := svc.DenyTable("analyst", "financials", auth.PrivSelect); err != nil {
		t.Fatalf("DenyTable failed: %v", err)
	}

	allowed, err = svc.HasTablePermission("alice_user", "financials", auth.PrivSelect)
	if err != nil {
		t.Fatalf("HasTablePermission failed: %v", err)
	}
	if allowed {
		t.Fatalf("expected alice_user to be denied SELECT on financials after role DENY")
	}
}

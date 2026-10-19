package auth_test

import (
	"testing"

	"sqlion/pkg/auth"
)

func TestRBAC_LoginAndUserLifecycle(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{})

	// 1. Create Login
	login, err := svc.CreateLogin("alice", "Secret123!")
	if err != nil {
		t.Fatalf("CreateLogin failed: %v", err)
	}
	if login.Name != "alice" {
		t.Errorf("expected login name alice, got %s", login.Name)
	}

	// Duplicate login should fail
	if _, err := svc.CreateLogin("alice", "Another"); err == nil {
		t.Errorf("expected duplicate login to fail")
	}

	// 2. Authenticate Login
	authLogin, err := svc.AuthenticateLogin("alice", "Secret123!")
	if err != nil || authLogin == nil {
		t.Fatalf("AuthenticateLogin failed: %v", err)
	}

	// Wrong password
	if _, err := svc.AuthenticateLogin("alice", "wrong"); err == nil {
		t.Errorf("expected wrong password to fail")
	}

	// 3. Alter Login Password
	if err := svc.AlterLoginPassword("alice", "NewPass456!"); err != nil {
		t.Fatalf("AlterLoginPassword failed: %v", err)
	}
	if _, err := svc.AuthenticateLogin("alice", "NewPass456!"); err != nil {
		t.Fatalf("AuthenticateLogin with new password failed: %v", err)
	}

	// 4. Create User for Login
	u, err := svc.CreateUser("alice_user", "alice", "public")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if u.Name != "alice_user" || u.LoginName != "alice" {
		t.Errorf("unexpected user attributes: %+v", u)
	}

	// User mapped to non-existent login should fail
	if _, err := svc.CreateUser("bob_user", "non_existent_login"); err == nil {
		t.Errorf("expected user with invalid login to fail")
	}

	// 5. Drop User and Login
	if err := svc.DropUser("alice_user"); err != nil {
		t.Fatalf("DropUser failed: %v", err)
	}
	if _, err := svc.GetUser("alice_user"); err == nil {
		t.Errorf("expected user to be deleted")
	}

	if err := svc.DropLogin("alice"); err != nil {
		t.Fatalf("DropLogin failed: %v", err)
	}
	if _, err := svc.GetLogin("alice"); err == nil {
		t.Errorf("expected login to be deleted")
	}

	// Built-in admin login cannot be dropped
	if err := svc.DropLogin("sa"); err == nil {
		t.Errorf("expected dropping sa login to fail")
	}
}

func TestRBAC_TablePermissionsGrantRevokeDeny(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{})

	_, _ = svc.CreateLogin("analyst", "pass")
	_, err := svc.CreateUser("analyst", "analyst")
	if err != nil {
		t.Fatalf("CreateUser analyst failed: %v", err)
	}

	// Initially analyst should have no permissions on table 'orders'
	allowed, err := svc.HasTablePermission("analyst", "orders", auth.PrivSelect)
	if err != nil {
		t.Fatalf("HasTablePermission failed: %v", err)
	}
	if allowed {
		t.Errorf("expected no SELECT permission initially")
	}

	// 1. Grant SELECT on orders
	if err := svc.GrantTable("analyst", "orders", auth.PrivSelect); err != nil {
		t.Fatalf("GrantTable failed: %v", err)
	}
	allowed, _ = svc.HasTablePermission("analyst", "orders", auth.PrivSelect)
	if !allowed {
		t.Errorf("expected SELECT permission after GRANT")
	}
	// INSERT should still be denied
	allowed, _ = svc.HasTablePermission("analyst", "orders", auth.PrivInsert)
	if allowed {
		t.Errorf("expected INSERT permission to be false")
	}

	// 2. Deny takes precedence over Grant
	if err := svc.DenyTable("analyst", "orders", auth.PrivSelect); err != nil {
		t.Fatalf("DenyTable failed: %v", err)
	}
	allowed, _ = svc.HasTablePermission("analyst", "orders", auth.PrivSelect)
	if allowed {
		t.Errorf("expected SELECT to be denied after DENY")
	}

	// 3. Grant ALL on customers
	if err := svc.GrantTable("analyst", "customers", auth.PrivAll); err != nil {
		t.Fatalf("GrantTable ALL failed: %v", err)
	}
	for _, priv := range []auth.Privilege{auth.PrivSelect, auth.PrivInsert, auth.PrivUpdate, auth.PrivDelete} {
		allowed, _ = svc.HasTablePermission("analyst", "customers", priv)
		if !allowed {
			t.Errorf("expected %s permission under ALL", priv)
		}
	}

	// 4. Revoke SELECT removes ALL
	if err := svc.RevokeTable("analyst", "customers", auth.PrivSelect); err != nil {
		t.Fatalf("RevokeTable failed: %v", err)
	}
	allowed, _ = svc.HasTablePermission("analyst", "customers", auth.PrivSelect)
	if allowed {
		t.Errorf("expected SELECT permission revoked")
	}

	// 5. Admin bypass
	for _, adminUser := range []string{"sa", "admin", "dbo", ""} {
		allowed, _ = svc.HasTablePermission(adminUser, "any_secret_table", auth.PrivDelete)
		if !allowed {
			t.Errorf("expected admin %q to bypass all checks", adminUser)
		}
	}
}

func TestRBAC_UnifiedSuperuserLogins(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{})
	_, err := svc.EnsureDefaultSuperuser("admin@lionsql.local", "SuperSecret123!")
	if err != nil {
		t.Fatalf("EnsureDefaultSuperuser failed: %v", err)
	}

	// 1. All built-in administrator logins must succeed with the single superuser password
	for _, name := range []string{"sa", "postgres", "admin", "SA", "Postgres"} {
		login, err := svc.AuthenticateLogin(name, "SuperSecret123!")
		if err != nil || login == nil {
			t.Fatalf("AuthenticateLogin for %q failed with superuser password: %v", name, err)
		}
	}

	// 2. Wrong password must fail
	for _, name := range []string{"sa", "postgres", "admin"} {
		if _, err := svc.AuthenticateLogin(name, "WrongPassword"); err == nil {
			t.Fatalf("AuthenticateLogin for %q unexpectedly succeeded with wrong password", name)
		}
	}

	// 3. Altering sa password synchronizes the superuser password
	if err := svc.AlterLoginPassword("sa", "NewSecret456!"); err != nil {
		t.Fatalf("AlterLoginPassword failed: %v", err)
	}

	// Superuser HTTP authentication must now work with new password
	su, _, err := svc.Authenticate("admin@lionsql.local", "NewSecret456!")
	if err != nil || su == nil {
		t.Fatalf("HTTP Superuser authenticate failed after AlterLoginPassword: %v", err)
	}

	// Wire logins (sa, postgres, admin) must now all require the new password
	for _, name := range []string{"sa", "postgres", "admin"} {
		if _, err := svc.AuthenticateLogin(name, "NewSecret456!"); err != nil {
			t.Fatalf("AuthenticateLogin for %q with updated password failed: %v", name, err)
		}
		if _, err := svc.AuthenticateLogin(name, "SuperSecret123!"); err == nil {
			t.Fatalf("AuthenticateLogin for %q unexpectedly accepted old password", name)
		}
	}

	// 4. Built-in postgres login cannot be dropped
	if err := svc.DropLogin("postgres"); err == nil {
		t.Fatalf("expected dropping postgres login to fail")
	}
}

type mockExternalProvider struct {
	users map[string]string // user -> password
}

func (m *mockExternalProvider) Name() string { return "mock_ldap" }

func (m *mockExternalProvider) Authenticate(username, password string) (*auth.Login, error) {
	if expected, ok := m.users[username]; ok && expected == password {
		return &auth.Login{
			Name:       username,
			IsDisabled: false,
		}, nil
	}
	return nil, auth.ErrInvalidCredentials
}

func TestRBAC_ExternalAuthProvider(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{})
	_, _ = svc.EnsureDefaultSuperuser("admin@lionsql.local", "MasterPass!")

	mockLDAP := &mockExternalProvider{
		users: map[string]string{
			"ldap_user": "CorporatePass123!",
		},
	}
	svc.RegisterProvider(mockLDAP)

	if len(svc.Providers()) != 1 {
		t.Fatalf("expected 1 provider registered")
	}

	// 1. External LDAP user succeeds with corporate password
	login, err := svc.AuthenticateLogin("ldap_user", "CorporatePass123!")
	if err != nil || login == nil {
		t.Fatalf("expected external provider authentication to succeed: %v", err)
	}

	// 2. Wrong corporate password fails
	if _, err := svc.AuthenticateLogin("ldap_user", "WrongPass"); err == nil {
		t.Fatalf("expected wrong password to fail")
	}

	// 3. Local built-in admin still takes precedence
	saLogin, err := svc.AuthenticateLogin("sa", "MasterPass!")
	if err != nil || saLogin == nil {
		t.Fatalf("expected built-in sa login to succeed with master pass")
	}
}

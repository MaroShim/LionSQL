package engine

import (
	"strings"
	"sync"
	"testing"

	"lionsql/pkg/auth"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestAuth_FailClosed_WhenAuthServiceNil verifies that if the authentication service
// is missing or uninitialized (s.auth == nil), external client sessions are strictly DENIED
// with an explicit fail-closed error rather than silently bypassed.
func TestAuth_FailClosed_WhenAuthServiceNil(t *testing.T) {
	var eng storage.Engine = memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())

	// 1. Setup table and initial data via internal system calls (cs == nil)
	if _, err := coord.ExecuteSQL("CREATE TABLE vault (id INT PRIMARY KEY, secret VARCHAR(64));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO vault VALUES (1, 'classified_secret');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Simulate abnormal/missing auth service
	coord.SetAuthService(nil, false)
	if coord.AuthService() != nil {
		t.Fatalf("expected AuthService to be nil after SetAuthService(nil, false)")
	}

	// 3. Register an external client session for "alice"
	clientSess, err := coord.GetOrCreateSession("client-sess-alice")
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	clientSess.User = "alice"

	// 4. Verify SELECT is explicitly rejected under Fail-Closed policy
	_, err = coord.ExecuteSessionSQL("client-sess-alice", "SELECT * FROM vault;")
	if err == nil {
		t.Fatalf("[CRITICAL SECURITY FAIL-OPEN] SELECT succeeded when s.auth == nil; expected fail-closed rejection")
	}
	if !strings.Contains(err.Error(), "authentication service is uninitialized (fail-closed)") {
		t.Fatalf("expected fail-closed error, got: %v", err)
	}

	// 5. Verify INSERT is explicitly rejected under Fail-Closed policy
	_, err = coord.ExecuteSessionSQL("client-sess-alice", "INSERT INTO vault VALUES (2, 'leak');")
	if err == nil {
		t.Fatalf("[CRITICAL SECURITY FAIL-OPEN] INSERT succeeded when s.auth == nil; expected fail-closed rejection")
	}
	if !strings.Contains(err.Error(), "authentication service is uninitialized (fail-closed)") {
		t.Fatalf("expected fail-closed error, got: %v", err)
	}

	// 6. Verify UPDATE is explicitly rejected under Fail-Closed policy
	_, err = coord.ExecuteSessionSQL("client-sess-alice", "UPDATE vault SET secret = 'pawned' WHERE id = 1;")
	if err == nil {
		t.Fatalf("[CRITICAL SECURITY FAIL-OPEN] UPDATE succeeded when s.auth == nil; expected fail-closed rejection")
	}
	if !strings.Contains(err.Error(), "authentication service is uninitialized (fail-closed)") {
		t.Fatalf("expected fail-closed error, got: %v", err)
	}

	// 7. Verify DELETE is explicitly rejected under Fail-Closed policy
	_, err = coord.ExecuteSessionSQL("client-sess-alice", "DELETE FROM vault WHERE id = 1;")
	if err == nil {
		t.Fatalf("[CRITICAL SECURITY FAIL-OPEN] DELETE succeeded when s.auth == nil; expected fail-closed rejection")
	}
	if !strings.Contains(err.Error(), "authentication service is uninitialized (fail-closed)") {
		t.Fatalf("expected fail-closed error, got: %v", err)
	}

	// 8. Verify direct in-process system calls (cs == nil) still execute cleanly
	if err := coord.checkTableAccess(nil, "vault", auth.PrivSelect); err != nil {
		t.Fatalf("in-process direct system call (cs == nil) should be permitted, got: %v", err)
	}

	// 9. Re-attach genuine AuthService and verify normal RBAC behavior
	normalAuth := auth.NewAuthService(auth.Config{})
	_, _ = normalAuth.CreateLogin("alice", "Pass1234!")
	_, _ = normalAuth.CreateUser("alice", "alice")
	coord.SetAuthService(normalAuth, true)

	// alice still has no grant on vault -> normal permission denied
	_, err = coord.ExecuteSessionSQL("client-sess-alice", "SELECT * FROM vault;")
	if err == nil || !strings.Contains(err.Error(), "permission denied for table") {
		t.Fatalf("expected normal permission denied for unprivileged alice, got: %v", err)
	}

	// Grant SELECT to alice -> now succeeds
	if err := normalAuth.GrantTable("alice", "vault", auth.PrivSelect); err != nil {
		t.Fatalf("GrantTable failed: %v", err)
	}
	res, err := coord.ExecuteSessionSQL("client-sess-alice", "SELECT id, secret FROM vault;")
	if err != nil {
		t.Fatalf("SELECT failed after GRANT: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
}

// TestAuth_CheckTableAccess_ConcurrentRace verifies that concurrent calls to SetSessionUser
// and checkTableAccess (via ExecuteSessionSQL) do not trigger Go runtime data races on cs.User (AUDIT-FULL-019).
func TestAuth_CheckTableAccess_ConcurrentRace(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())

	if _, err := coord.ExecuteSQL("CREATE TABLE test_race (id INT PRIMARY KEY, val VARCHAR(64));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	authSvc := auth.NewAuthService(auth.Config{})
	_, _ = authSvc.CreateLogin("alice", "Pass1234!")
	_, _ = authSvc.CreateUser("alice", "alice")
	_ = authSvc.GrantTable("alice", "test_race", auth.PrivSelect)
	coord.SetAuthService(authSvc, true)

	sessID := "sess-race-user"
	sess, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	sess.User = "alice"

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Goroutine 1: Rapidly mutate cs.User via SetSessionUser
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				if i%2 == 0 {
					coord.SetSessionUser(sessID, "alice")
				} else {
					coord.SetSessionUser(sessID, "bob")
				}
			}
		}
	}()

	// Goroutine 2: Repeatedly run queries triggering checkTableAccess
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = coord.ExecuteSessionSQL(sessID, "SELECT * FROM test_race;")
		}
		close(stop)
	}()

	wg.Wait()
}

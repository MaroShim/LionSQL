package replication_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lionsql/pkg/replication"
	"lionsql/pkg/storage/memengine"
)

func TestHandlers_AuthenticationEnforcement_AuditFull040(t *testing.T) {
	const secretToken = "cluster-secret-key-xyz-789"
	const adminJWT = "valid-super-admin-jwt-token"

	eng := memengine.New()
	defer eng.Close()

	mgr, err := replication.NewManager(replication.Config{
		NodeID:       "node-primary",
		Role:         replication.RolePrimary,
		Engine:       eng,
		ClusterToken: secretToken,
		AuthValidator: func(token string) bool {
			return token == adminJWT
		},
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	mux := http.NewServeMux()
	mgr.RegisterRoutes(mux)

	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/replication/status", ""},
		{"POST", "/api/replication/ack", `{"node_id":"node-2","acked_lsn":10}`},
		{"POST", "/api/replication/promote", `{"term":2}`},
		{"POST", "/api/replication/demote", `{"primary_url":"http://127.0.0.1:8099"}`},
		{"POST", "/api/replication/vote", `{"term":2,"candidate_id":"node-2","candidate_url":"http://127.0.0.1:8099","last_lsn":10}`},
		{"POST", "/api/replication/leader", `{"term":2,"leader_id":"node-2","leader_url":"http://127.0.0.1:8099"}`},
		{"GET", "/api/replication/stream?from_lsn=0&replica_id=test-replica", ""},
	}

	// 1. Unauthenticated requests should all be blocked with 401 Unauthorized
	for _, ep := range endpoints {
		t.Run("Unauthenticated_"+ep.method+"_"+ep.path, func(t *testing.T) {
			var bodyReader io.Reader
			if ep.body != "" {
				bodyReader = bytes.NewBufferString(ep.body)
			}
			req := httptest.NewRequest(ep.method, ep.path, bodyReader)
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 Unauthorized for unauthenticated %s %s, got %d", ep.method, ep.path, rec.Code)
			}
		})
	}

	// 2. Request with invalid token should be blocked with 401 Unauthorized
	for _, ep := range endpoints {
		t.Run("InvalidToken_"+ep.method+"_"+ep.path, func(t *testing.T) {
			var bodyReader io.Reader
			if ep.body != "" {
				bodyReader = bytes.NewBufferString(ep.body)
			}
			req := httptest.NewRequest(ep.method, ep.path, bodyReader)
			req.Header.Set("X-Cluster-Token", "wrong-invalid-token")
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 Unauthorized for invalid token %s %s, got %d", ep.method, ep.path, rec.Code)
			}
		})
	}

	// 3. Request with valid X-Cluster-Token should pass auth check
	for _, ep := range endpoints {
		t.Run("ValidXClusterToken_"+ep.method+"_"+ep.path, func(t *testing.T) {
			var bodyReader io.Reader
			if ep.body != "" {
				bodyReader = bytes.NewBufferString(ep.body)
			}
			req := httptest.NewRequest(ep.method, ep.path, bodyReader)
			req.Header.Set("X-Cluster-Token", secretToken)
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("expected authorization to succeed with X-Cluster-Token for %s %s, got 401", ep.method, ep.path)
			}
		})
	}

	// 4. Request with valid ?cluster_token= query parameter should pass auth check
	t.Run("ValidClusterTokenQueryParam", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/replication/status?cluster_token="+secretToken, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK with ?cluster_token= param, got %d", rec.Code)
		}
	})

	// 5. Request with Authorization: Bearer <clusterToken> should pass auth check
	t.Run("ValidBearerClusterToken", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/replication/status", nil)
		req.Header.Set("Authorization", "Bearer "+secretToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK with Bearer cluster token, got %d", rec.Code)
		}
	})

	// 6. Request with Authorization: Bearer <adminJWT> should pass auth check via AuthValidator
	t.Run("ValidAdminJWTBearer", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/replication/status", nil)
		req.Header.Set("Authorization", "Bearer "+adminJWT)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK with admin JWT bearer, got %d", rec.Code)
		}
	})
}

func TestHandlers_PermissiveModeWhenNoTokenConfigured(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	// Default manager with no cluster token
	mgr, err := replication.NewManager(replication.Config{
		NodeID: "node-permissive",
		Role:   replication.RolePrimary,
		Engine: eng,
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	mux := http.NewServeMux()
	mgr.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/replication/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK in permissive mode without token, got %d", rec.Code)
	}
}

func TestReplication_ClusterTokenInterNodeStreaming_AuditFull040(t *testing.T) {
	const clusterToken = "node-mesh-secret-key-456"

	engPrimary := memengine.New()
	defer engPrimary.Close()

	mgrPrimary, err := replication.NewManager(replication.Config{
		NodeID:       "primary-node",
		Role:         replication.RolePrimary,
		Engine:       engPrimary,
		ClusterToken: clusterToken,
	})
	if err != nil {
		t.Fatalf("failed to create primary manager: %v", err)
	}

	mux := http.NewServeMux()
	mgrPrimary.RegisterRoutes(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Replica with matching token should successfully stream
	engReplica1 := memengine.New()
	defer engReplica1.Close()

	client1 := replication.NewReplicationClient(ts.URL, "replica-1", engReplica1, 0)
	client1.SetClusterToken(clusterToken)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client1.Start(ctx)
	defer client1.Stop()

	// Wait briefly for stream connection to establish
	time.Sleep(50 * time.Millisecond)

	status := mgrPrimary.Status()
	found := false
	for _, rep := range status.Replicas {
		if rep.NodeID == "replica-1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected replica-1 to connect and stream with valid cluster token, current replicas: %+v", status.Replicas)
	}

	// 2. Replica with mismatched token should fail to connect
	engReplica2 := memengine.New()
	defer engReplica2.Close()

	client2 := replication.NewReplicationClient(ts.URL, "replica-bad", engReplica2, 0)
	client2.SetClusterToken("wrong-cluster-token")

	client2.Start(ctx)
	defer client2.Stop()

	time.Sleep(50 * time.Millisecond)

	status = mgrPrimary.Status()
	for _, rep := range status.Replicas {
		if rep.NodeID == "replica-bad" {
			t.Fatalf("replica with invalid token should not have been authorized into streaming replicas list")
		}
	}
}

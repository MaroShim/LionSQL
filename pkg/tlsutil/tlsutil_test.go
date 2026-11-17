package tlsutil_test

import (
	"crypto/tls"
	"testing"

	"lionsql/pkg/tlsutil"
)

func TestNewSelfSignedTLSConfig(t *testing.T) {
	cfg, err := tlsutil.NewSelfSignedTLSConfig()
	if err != nil {
		t.Fatalf("failed to generate self-signed TLS config: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config")
	}
	if len(cfg.Certificates) == 0 {
		t.Fatal("expected at least one certificate")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("expected MinVersion TLS 1.2, got %d", cfg.MinVersion)
	}
}

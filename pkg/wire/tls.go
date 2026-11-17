package wire

import (
	"crypto/tls"

	"lionsql/pkg/tlsutil"
)

// GenerateSelfSignedCert generates an in-memory self-signed TLS certificate for localhost/testing.
func GenerateSelfSignedCert() (tls.Certificate, error) {
	return tlsutil.GenerateSelfSignedCert()
}

// NewSelfSignedTLSConfig returns a TLS configuration using an auto-generated self-signed certificate.
func NewSelfSignedTLSConfig() (*tls.Config, error) {
	return tlsutil.NewSelfSignedTLSConfig()
}

// LoadTLSConfig loads certificate and private key from file paths.
func LoadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	return tlsutil.LoadTLSConfig(certFile, keyFile)
}

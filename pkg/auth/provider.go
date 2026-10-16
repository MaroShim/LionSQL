package auth

// AuthProvider abstracts identity verification for local and external directory services (e.g. LDAP, Active Directory).
// It enables SQLion to plug in enterprise directory authentication without coupling the database kernel to external network dependencies.
type AuthProvider interface {
	// Name returns the unique provider identifier (e.g. "local", "ldap").
	Name() string

	// Authenticate validates the provided credentials and returns an authenticated Login principal if valid.
	Authenticate(username, password string) (*Login, error)
}

package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrLoginNotFound    = errors.New("login not found")
	ErrLoginExists      = errors.New("login already exists")
	ErrDBUserNotFound   = errors.New("database user not found")
	ErrDBUserExists     = errors.New("database user already exists")
	ErrInvalidPrivilege = errors.New("invalid privilege")
)

// Privilege represents an authorization action on database objects.
type Privilege string

const (
	PrivSelect Privilege = "SELECT"
	PrivInsert Privilege = "INSERT"
	PrivUpdate Privilege = "UPDATE"
	PrivDelete Privilege = "DELETE"
	PrivAll    Privilege = "ALL"
)

// ParsePrivilege parses a privilege string case-insensitively.
func ParsePrivilege(s string) (Privilege, error) {
	upper := strings.ToUpper(strings.TrimSpace(s))
	switch upper {
	case "SELECT":
		return PrivSelect, nil
	case "INSERT":
		return PrivInsert, nil
	case "UPDATE":
		return PrivUpdate, nil
	case "DELETE":
		return PrivDelete, nil
	case "ALL", "ALL PRIVILEGES":
		return PrivAll, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidPrivilege, s)
	}
}

// GrantType distinguishes between GRANT (allow) and DENY.
type GrantType int

const (
	GrantAllow GrantType = 1
	GrantDeny  GrantType = 2
)

// Login represents an instance-level server principal.
type Login struct {
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	IsDisabled   bool      `json:"is_disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// User represents a database-level principal mapped to a login.
type User struct {
	Name      string    `json:"name"`
	LoginName string    `json:"login_name"`
	Roles     []string  `json:"roles"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateLogin registers a new server login principal.
func (s *AuthService) CreateLogin(name, password string) (*Login, error) {
	norm := strings.ToLower(strings.TrimSpace(name))
	if norm == "" {
		return nil, errors.New("login name cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.logins[norm]; exists {
		return nil, fmt.Errorf("%w: %s", ErrLoginExists, name)
	}

	pwHash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	l := &Login{
		Name:         norm,
		PasswordHash: pwHash,
		IsDisabled:   false,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.logins[norm] = l
	return l, nil
}

// AlterLoginPassword updates the password for an existing login.
func (s *AuthService) AlterLoginPassword(name, newPassword string) error {
	norm := strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	defer s.mu.Unlock()

	l, exists := s.logins[norm]
	if !exists {
		return fmt.Errorf("%w: %s", ErrLoginNotFound, name)
	}

	pwHash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}

	l.PasswordHash = pwHash
	l.UpdatedAt = time.Now().UTC()
	return nil
}

// DropLogin removes a server login principal.
func (s *AuthService) DropLogin(name string) error {
	norm := strings.ToLower(strings.TrimSpace(name))
	if norm == "sa" || norm == "admin" {
		return errors.New("cannot drop built-in administrator login")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.logins[norm]; !exists {
		return fmt.Errorf("%w: %s", ErrLoginNotFound, name)
	}
	delete(s.logins, norm)
	return nil
}

// GetLogin retrieves a server login by name.
func (s *AuthService) GetLogin(name string) (*Login, error) {
	norm := strings.ToLower(strings.TrimSpace(name))
	s.mu.RLock()
	defer s.mu.RUnlock()

	l, exists := s.logins[norm]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrLoginNotFound, name)
	}
	return l, nil
}

// AuthenticateLogin verifies login name and password.
func (s *AuthService) AuthenticateLogin(name, password string) (*Login, error) {
	norm := strings.ToLower(strings.TrimSpace(name))
	s.mu.RLock()
	l, exists := s.logins[norm]
	s.mu.RUnlock()

	if !exists || l.IsDisabled || !VerifyPassword(password, l.PasswordHash) {
		return nil, ErrInvalidCredentials
	}
	return l, nil
}

// CreateUser registers a database-level principal mapped to a login.
func (s *AuthService) CreateUser(name, loginName string, roles ...string) (*User, error) {
	normUser := strings.ToLower(strings.TrimSpace(name))
	normLogin := strings.ToLower(strings.TrimSpace(loginName))
	if normUser == "" {
		return nil, errors.New("user name cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.dbUsers[normUser]; exists {
		return nil, fmt.Errorf("%w: %s", ErrDBUserExists, name)
	}

	// Verify that the login exists if specified
	if normLogin != "" {
		if _, exists := s.logins[normLogin]; !exists {
			return nil, fmt.Errorf("%w: %s", ErrLoginNotFound, loginName)
		}
	} else {
		normLogin = normUser
	}

	now := time.Now().UTC()
	u := &User{
		Name:      normUser,
		LoginName: normLogin,
		Roles:     roles,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.dbUsers[normUser] = u
	return u, nil
}

// DropUser removes a database-level user principal and cleans up its grants.
func (s *AuthService) DropUser(name string) error {
	norm := strings.ToLower(strings.TrimSpace(name))
	if norm == "dbo" || norm == "guest" || norm == "sa" {
		return errors.New("cannot drop built-in database user")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.dbUsers[norm]; !exists {
		return fmt.Errorf("%w: %s", ErrDBUserNotFound, name)
	}
	delete(s.dbUsers, norm)
	delete(s.grants, norm)
	return nil
}

// GetUser retrieves a database user by name.
func (s *AuthService) GetUser(name string) (*User, error) {
	norm := strings.ToLower(strings.TrimSpace(name))
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, exists := s.dbUsers[norm]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrDBUserNotFound, name)
	}
	return u, nil
}

// GrantTable grants a privilege on a table to a database user.
func (s *AuthService) GrantTable(user, table string, priv Privilege) error {
	normUser := strings.ToLower(strings.TrimSpace(user))
	normTable := strings.ToLower(strings.TrimSpace(table))

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.dbUsers[normUser]; !exists && normUser != "public" && normUser != "guest" {
		return fmt.Errorf("%w: %s", ErrDBUserNotFound, user)
	}

	if s.grants[normUser] == nil {
		s.grants[normUser] = make(map[string]map[Privilege]GrantType)
	}
	if s.grants[normUser][normTable] == nil {
		s.grants[normUser][normTable] = make(map[Privilege]GrantType)
	}

	s.grants[normUser][normTable][priv] = GrantAllow
	return nil
}

// RevokeTable revokes a privilege on a table from a database user.
func (s *AuthService) RevokeTable(user, table string, priv Privilege) error {
	normUser := strings.ToLower(strings.TrimSpace(user))
	normTable := strings.ToLower(strings.TrimSpace(table))

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.dbUsers[normUser]; !exists && normUser != "public" && normUser != "guest" {
		return fmt.Errorf("%w: %s", ErrDBUserNotFound, user)
	}

	if s.grants[normUser] != nil && s.grants[normUser][normTable] != nil {
		if priv == PrivAll {
			delete(s.grants[normUser], normTable)
		} else {
			delete(s.grants[normUser][normTable], priv)
			// Also remove PrivAll if individual privilege revoked
			delete(s.grants[normUser][normTable], PrivAll)
		}
	}
	return nil
}

// DenyTable explicitly denies a privilege on a table to a database user.
func (s *AuthService) DenyTable(user, table string, priv Privilege) error {
	normUser := strings.ToLower(strings.TrimSpace(user))
	normTable := strings.ToLower(strings.TrimSpace(table))

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.dbUsers[normUser]; !exists && normUser != "public" && normUser != "guest" {
		return fmt.Errorf("%w: %s", ErrDBUserNotFound, user)
	}

	if s.grants[normUser] == nil {
		s.grants[normUser] = make(map[string]map[Privilege]GrantType)
	}
	if s.grants[normUser][normTable] == nil {
		s.grants[normUser][normTable] = make(map[Privilege]GrantType)
	}

	s.grants[normUser][normTable][priv] = GrantDeny
	return nil
}

// HasTablePermission checks if a user has the specified privilege on a table.
// Administrative principals (sa, admin, dbo) bypass all checks.
// Explicit DENY takes precedence over any GRANT.
func (s *AuthService) HasTablePermission(user, table string, priv Privilege) (bool, error) {
	normUser := strings.ToLower(strings.TrimSpace(user))
	normTable := strings.ToLower(strings.TrimSpace(table))

	// Administrator bypass
	if normUser == "" || normUser == "sa" || normUser == "admin" || normUser == "dbo" {
		return true, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	u, exists := s.dbUsers[normUser]
	if exists {
		// Check db_owner role
		for _, r := range u.Roles {
			if strings.EqualFold(r, "db_owner") {
				return true, nil
			}
			if strings.EqualFold(r, "db_datareader") && priv == PrivSelect {
				return true, nil
			}
			if strings.EqualFold(r, "db_datawriter") && (priv == PrivInsert || priv == PrivUpdate || priv == PrivDelete) {
				return true, nil
			}
		}
	}

	// Helper to inspect grants map for a subject (user, public)
	checkSubject := func(subj string) (hasGrant bool, isDenied bool) {
		if s.grants[subj] == nil {
			return false, false
		}
		tableGrants := s.grants[subj][normTable]
		if tableGrants == nil {
			// Check wildcard table grant "*"
			tableGrants = s.grants[subj]["*"]
		}
		if tableGrants == nil {
			return false, false
		}

		// Check explicit DENY first (Deny takes precedence)
		if tableGrants[priv] == GrantDeny || tableGrants[PrivAll] == GrantDeny {
			return false, true
		}

		// Check GRANT
		if tableGrants[priv] == GrantAllow || tableGrants[PrivAll] == GrantAllow {
			return true, false
		}
		return false, false
	}

	// 1. Check user-specific grants
	userGranted, userDenied := checkSubject(normUser)
	if userDenied {
		return false, nil
	}
	if userGranted {
		return true, nil
	}

	// 2. Check public grants
	pubGranted, pubDenied := checkSubject("public")
	if pubDenied {
		return false, nil
	}
	if pubGranted {
		return true, nil
	}

	return false, nil
}

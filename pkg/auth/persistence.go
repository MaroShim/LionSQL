package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// persistedLogin captures serialized login metadata including credentials hash.
type persistedLogin struct {
	Name         string    `json:"name"`
	PasswordHash string    `json:"password_hash"`
	IsDisabled   bool      `json:"is_disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// persistedSuperuser captures serialized administrative user metadata.
type persistedSuperuser struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name,omitempty"`
	AvatarURL    string    `json:"avatar_url,omitempty"`
	PasswordHash string    `json:"password_hash"`
	TokenKey     string    `json:"token_key"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// persistedAPIKey captures serialized API key metadata including hashed secret.
type persistedAPIKey struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	HashedKey     string     `json:"hashed_key"`
	Role          APIKeyRole `json:"role"`
	AllowedTables []string   `json:"allowed_tables"`
	CreatedAt     time.Time  `json:"created_at"`
}

// authSnapshot is the root persistence envelope for auth.json ([RULE:AtomicSaves]).
type authSnapshot struct {
	Version           int                                           `json:"version"`
	DefaultAdminEmail string                                        `json:"default_admin_email,omitempty"`
	Logins            map[string]*persistedLogin                    `json:"logins"`
	DBUsers           map[string]*User                              `json:"db_users"`
	Grants            map[string]map[string]map[Privilege]GrantType `json:"grants"`
	Superusers        map[string]*persistedSuperuser                `json:"superusers"`
	APIKeys           map[string]*persistedAPIKey                   `json:"api_keys"`
	UpdatedAt         time.Time                                     `json:"updated_at"`
}

// saveLocked writes all authentication and RBAC metadata to auth.json atomically ([RULE:AtomicSaves]).
// Caller MUST hold s.mu.
func (s *AuthService) saveLocked() error {
	if s.dataFile == "" {
		return nil
	}

	snap := authSnapshot{
		Version:           1,
		DefaultAdminEmail: s.defaultAdminEmail,
		Logins:            make(map[string]*persistedLogin, len(s.logins)),
		DBUsers:           make(map[string]*User, len(s.dbUsers)),
		Grants:            make(map[string]map[string]map[Privilege]GrantType, len(s.grants)),
		Superusers:        make(map[string]*persistedSuperuser, len(s.users)),
		APIKeys:           make(map[string]*persistedAPIKey, len(s.apiKeys)),
		UpdatedAt:         time.Now().UTC(),
	}

	for k, v := range s.logins {
		snap.Logins[k] = &persistedLogin{
			Name:         v.Name,
			PasswordHash: v.PasswordHash,
			IsDisabled:   v.IsDisabled,
			CreatedAt:    v.CreatedAt,
			UpdatedAt:    v.UpdatedAt,
		}
	}

	for k, v := range s.dbUsers {
		snap.DBUsers[k] = &User{
			Name:      v.Name,
			LoginName: v.LoginName,
			Roles:     append([]string(nil), v.Roles...),
			CreatedAt: v.CreatedAt,
			UpdatedAt: v.UpdatedAt,
		}
	}

	for u, tables := range s.grants {
		snap.Grants[u] = make(map[string]map[Privilege]GrantType, len(tables))
		for tbl, privs := range tables {
			snap.Grants[u][tbl] = make(map[Privilege]GrantType, len(privs))
			for p, g := range privs {
				snap.Grants[u][tbl][p] = g
			}
		}
	}

	for k, v := range s.users {
		snap.Superusers[k] = &persistedSuperuser{
			ID:           v.ID,
			Email:        v.Email,
			Name:         v.Name,
			AvatarURL:    v.AvatarURL,
			PasswordHash: v.PasswordHash,
			TokenKey:     v.TokenKey,
			CreatedAt:    v.CreatedAt,
			UpdatedAt:    v.UpdatedAt,
		}
	}

	for k, v := range s.apiKeys {
		snap.APIKeys[k] = &persistedAPIKey{
			ID:            v.ID,
			Name:          v.Name,
			HashedKey:     v.HashedKey,
			Role:          v.Role,
			AllowedTables: append([]string(nil), v.AllowedTables...),
			CreatedAt:     v.CreatedAt,
		}
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal auth metadata: %w", err)
	}

	dir := filepath.Dir(s.dataFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create auth directory: %w", err)
	}

	tmpFile := s.dataFile + ".tmp"
	// Write with restricted permissions (0600) for security sensitive credentials
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write auth temp file: %w", err)
	}

	if err := os.Rename(tmpFile, s.dataFile); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to atomically commit auth file: %w", err)
	}

	return nil
}

// loadLocked reads and restores metadata from auth.json if present ([RULE:AtomicSaves]).
// Caller MUST hold s.mu. Returns true if metadata was successfully restored from disk.
func (s *AuthService) loadLocked() (bool, error) {
	if s.dataFile == "" {
		return false, nil
	}

	cleanPath := filepath.Clean(s.dataFile)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read auth file: %w", err)
	}

	var snap authSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return false, fmt.Errorf("failed to unmarshal auth metadata from %s: %w", cleanPath, err)
	}

	if snap.DefaultAdminEmail != "" {
		s.defaultAdminEmail = snap.DefaultAdminEmail
	}

	if snap.Logins != nil {
		for k, v := range snap.Logins {
			s.logins[k] = &Login{
				Name:         v.Name,
				PasswordHash: v.PasswordHash,
				IsDisabled:   v.IsDisabled,
				CreatedAt:    v.CreatedAt,
				UpdatedAt:    v.UpdatedAt,
			}
		}
	}

	if snap.DBUsers != nil {
		for k, v := range snap.DBUsers {
			s.dbUsers[k] = &User{
				Name:      v.Name,
				LoginName: v.LoginName,
				Roles:     append([]string(nil), v.Roles...),
				CreatedAt: v.CreatedAt,
				UpdatedAt: v.UpdatedAt,
			}
		}
	}

	if snap.Grants != nil {
		for u, tables := range snap.Grants {
			if s.grants[u] == nil {
				s.grants[u] = make(map[string]map[Privilege]GrantType)
			}
			for tbl, privs := range tables {
				if s.grants[u][tbl] == nil {
					s.grants[u][tbl] = make(map[Privilege]GrantType)
				}
				for p, g := range privs {
					s.grants[u][tbl][p] = g
				}
			}
		}
	}

	if snap.Superusers != nil {
		for k, v := range snap.Superusers {
			su := &Superuser{
				ID:           v.ID,
				Email:        v.Email,
				Name:         v.Name,
				AvatarURL:    v.AvatarURL,
				PasswordHash: v.PasswordHash,
				TokenKey:     v.TokenKey,
				CreatedAt:    v.CreatedAt,
				UpdatedAt:    v.UpdatedAt,
			}
			s.users[k] = su
			s.usersByID[v.ID] = su
		}
	}

	if snap.APIKeys != nil {
		for k, v := range snap.APIKeys {
			ak := &APIKey{
				ID:            v.ID,
				Name:          v.Name,
				HashedKey:     v.HashedKey,
				Role:          v.Role,
				AllowedTables: append([]string(nil), v.AllowedTables...),
				CreatedAt:     v.CreatedAt,
			}
			s.apiKeys[k] = ak
			s.apiKeysByID[v.ID] = ak
		}
	}

	return true, nil
}

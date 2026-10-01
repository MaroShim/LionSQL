package api

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"sqlion/pkg/auth"
)

var (
	reCreateLogin = regexp.MustCompile(`(?is)^CREATE\s+LOGIN\s+([^\s]+)\s+WITH\s+PASSWORD\s*=\s*['"](.*?)['"]`)
	reAlterLogin  = regexp.MustCompile(`(?is)^ALTER\s+LOGIN\s+([^\s]+)\s+WITH\s+PASSWORD\s*=\s*['"](.*?)['"]`)
	reDropLogin   = regexp.MustCompile(`(?is)^DROP\s+LOGIN\s+(?:IF\s+EXISTS\s+)?([^\s;]+)`)
	reCreateUser  = regexp.MustCompile(`(?is)^CREATE\s+USER\s+([^\s]+)(?:\s+FOR\s+LOGIN\s+([^\s;]+))?(?:\s+WITH\s+PASSWORD\s*=\s*['"](.*?)['"])?`)
	reDropUser    = regexp.MustCompile(`(?is)^DROP\s+USER\s+(?:IF\s+EXISTS\s+)?([^\s;]+)`)
	reGrant       = regexp.MustCompile(`(?is)^GRANT\s+(.+?)\s+ON\s+(?:TABLE\s+)?([^\s]+)\s+TO\s+([^\s;]+)`)
	reRevoke      = regexp.MustCompile(`(?is)^REVOKE\s+(.+?)\s+ON\s+(?:TABLE\s+)?([^\s]+)\s+FROM\s+([^\s;]+)`)
	reDeny        = regexp.MustCompile(`(?is)^DENY\s+(.+?)\s+ON\s+(?:TABLE\s+)?([^\s]+)\s+TO\s+([^\s;]+)`)
	reSetUser     = regexp.MustCompile(`(?is)^SETUSER\s+['"](.*?)['"]`)
	reExecAs      = regexp.MustCompile(`(?is)^EXEC(?:UTE)?\s+AS\s+USER\s*=\s*['"](.*?)['"]`)
)

// checkTableAccess checks whether the current session user has permission to access a relation.
func (s *Server) checkTableAccess(cs *ClientSession, table string, priv auth.Privilege) error {
	if s.auth == nil {
		return nil
	}
	cleanTbl := cleanIdentifier(table)
	// Virtual system catalog views or temporary tables bypass custom table grants
	if strings.HasPrefix(strings.ToLower(cleanTbl), "sys.") ||
		strings.HasPrefix(strings.ToLower(cleanTbl), "system.") ||
		strings.HasPrefix(strings.ToLower(cleanTbl), "information_schema.") ||
		strings.HasPrefix(cleanTbl, "#") {
		return nil
	}

	user := ""
	if cs != nil {
		user = cs.User
	}
	allowed, err := s.auth.HasTablePermission(user, cleanTbl, priv)
	if err != nil {
		return err
	}
	if !allowed {
		if user == "" {
			user = "anonymous"
		}
		return fmt.Errorf("permission denied for table %q (action: %s, user: %q)", cleanTbl, priv, user)
	}
	return nil
}

// execCreateLogin handles CREATE LOGIN <name> WITH PASSWORD = '<password>'.
func (s *Server) execCreateLogin(query string, start time.Time) (*QueryResult, error) {
	m := reCreateLogin.FindStringSubmatch(query)
	if len(m) < 3 {
		return nil, fmt.Errorf("invalid syntax: expected CREATE LOGIN <login_name> WITH PASSWORD = '<password>'")
	}
	loginName := cleanIdentifier(m[1])
	password := m[2]

	_, err := s.auth.CreateLogin(loginName, password)
	if err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "CREATE LOGIN",
		RowsAffected: 0,
		Messages:     []string{fmt.Sprintf("Login %q created successfully.", loginName)},
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execAlterLogin handles ALTER LOGIN <name> WITH PASSWORD = '<password>'.
func (s *Server) execAlterLogin(query string, start time.Time) (*QueryResult, error) {
	m := reAlterLogin.FindStringSubmatch(query)
	if len(m) < 3 {
		return nil, fmt.Errorf("invalid syntax: expected ALTER LOGIN <login_name> WITH PASSWORD = '<password>'")
	}
	loginName := cleanIdentifier(m[1])
	password := m[2]

	err := s.auth.AlterLoginPassword(loginName, password)
	if err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "ALTER LOGIN",
		RowsAffected: 0,
		Messages:     []string{fmt.Sprintf("Login %q updated successfully.", loginName)},
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execDropLogin handles DROP LOGIN [IF EXISTS] <name>.
func (s *Server) execDropLogin(query string, start time.Time) (*QueryResult, error) {
	m := reDropLogin.FindStringSubmatch(query)
	if len(m) < 2 {
		return nil, fmt.Errorf("invalid syntax: expected DROP LOGIN [IF EXISTS] <login_name>")
	}
	loginName := cleanIdentifier(m[1])
	ifExists := strings.Contains(strings.ToUpper(query), "IF EXISTS")

	err := s.auth.DropLogin(loginName)
	if err != nil {
		if ifExists && strings.Contains(err.Error(), "not found") {
			return &QueryResult{
				Success:      true,
				Command:      "DROP LOGIN",
				RowsAffected: 0,
				Messages:     []string{fmt.Sprintf("Login %q does not exist (skipped).", loginName)},
				DurationUS:   time.Since(start).Microseconds(),
			}, nil
		}
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "DROP LOGIN",
		RowsAffected: 0,
		Messages:     []string{fmt.Sprintf("Login %q dropped successfully.", loginName)},
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execCreateUser handles CREATE USER <name> [FOR LOGIN <login>] [WITH PASSWORD = '<password>'].
func (s *Server) execCreateUser(query string, start time.Time) (*QueryResult, error) {
	m := reCreateUser.FindStringSubmatch(query)
	if len(m) < 2 {
		return nil, fmt.Errorf("invalid syntax: expected CREATE USER <user_name> [FOR LOGIN <login_name>]")
	}
	userName := cleanIdentifier(m[1])
	loginName := ""
	if len(m) >= 3 && m[2] != "" {
		loginName = cleanIdentifier(m[2])
	}
	password := ""
	if len(m) >= 4 && m[3] != "" {
		password = m[3]
	}

	// If password provided and no separate login, create the corresponding login automatically
	if password != "" && loginName == "" {
		loginName = userName
		_, _ = s.auth.CreateLogin(loginName, password)
	} else if loginName == "" {
		loginName = userName
	}

	_, err := s.auth.CreateUser(userName, loginName)
	if err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "CREATE USER",
		RowsAffected: 0,
		Messages:     []string{fmt.Sprintf("User %q created successfully.", userName)},
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execDropUser handles DROP USER [IF EXISTS] <name>.
func (s *Server) execDropUser(query string, start time.Time) (*QueryResult, error) {
	m := reDropUser.FindStringSubmatch(query)
	if len(m) < 2 {
		return nil, fmt.Errorf("invalid syntax: expected DROP USER [IF EXISTS] <user_name>")
	}
	userName := cleanIdentifier(m[1])
	ifExists := strings.Contains(strings.ToUpper(query), "IF EXISTS")

	err := s.auth.DropUser(userName)
	if err != nil {
		if ifExists && strings.Contains(err.Error(), "not found") {
			return &QueryResult{
				Success:      true,
				Command:      "DROP USER",
				RowsAffected: 0,
				Messages:     []string{fmt.Sprintf("User %q does not exist (skipped).", userName)},
				DurationUS:   time.Since(start).Microseconds(),
			}, nil
		}
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "DROP USER",
		RowsAffected: 0,
		Messages:     []string{fmt.Sprintf("User %q dropped successfully.", userName)},
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execGrant handles GRANT <permission> ON [TABLE] <table_name> TO <user_name>.
func (s *Server) execGrant(query string, start time.Time) (*QueryResult, error) {
	m := reGrant.FindStringSubmatch(query)
	if len(m) < 4 {
		return nil, fmt.Errorf("invalid syntax: expected GRANT <privilege> ON [TABLE] <table_name> TO <user_name>")
	}

	privStr := strings.TrimSpace(m[1])
	tableName := cleanIdentifier(m[2])
	userName := cleanIdentifier(m[3])

	priv, err := auth.ParsePrivilege(privStr)
	if err != nil {
		return nil, err
	}

	if err := s.auth.GrantTable(userName, tableName, priv); err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "GRANT",
		RowsAffected: 0,
		Messages:     []string{fmt.Sprintf("Permission %s granted on %q to %q.", priv, tableName, userName)},
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execRevoke handles REVOKE <permission> ON [TABLE] <table_name> FROM <user_name>.
func (s *Server) execRevoke(query string, start time.Time) (*QueryResult, error) {
	m := reRevoke.FindStringSubmatch(query)
	if len(m) < 4 {
		return nil, fmt.Errorf("invalid syntax: expected REVOKE <privilege> ON [TABLE] <table_name> FROM <user_name>")
	}

	privStr := strings.TrimSpace(m[1])
	tableName := cleanIdentifier(m[2])
	userName := cleanIdentifier(m[3])

	priv, err := auth.ParsePrivilege(privStr)
	if err != nil {
		return nil, err
	}

	if err := s.auth.RevokeTable(userName, tableName, priv); err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "REVOKE",
		RowsAffected: 0,
		Messages:     []string{fmt.Sprintf("Permission %s revoked on %q from %q.", priv, tableName, userName)},
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execDeny handles DENY <permission> ON [TABLE] <table_name> TO <user_name>.
func (s *Server) execDeny(query string, start time.Time) (*QueryResult, error) {
	m := reDeny.FindStringSubmatch(query)
	if len(m) < 4 {
		return nil, fmt.Errorf("invalid syntax: expected DENY <privilege> ON [TABLE] <table_name> TO <user_name>")
	}

	privStr := strings.TrimSpace(m[1])
	tableName := cleanIdentifier(m[2])
	userName := cleanIdentifier(m[3])

	priv, err := auth.ParsePrivilege(privStr)
	if err != nil {
		return nil, err
	}

	if err := s.auth.DenyTable(userName, tableName, priv); err != nil {
		return nil, err
	}

	return &QueryResult{
		Success:      true,
		Command:      "DENY",
		RowsAffected: 0,
		Messages:     []string{fmt.Sprintf("Permission %s denied on %q to %q.", priv, tableName, userName)},
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

// execSetUser handles SETUSER '<username>' / SETUSER / EXECUTE AS USER = '<username>' / REVERT.
func (s *Server) execSetUser(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	upper := strings.ToUpper(strings.TrimSpace(query))

	if cs == nil {
		return &QueryResult{Success: true, Command: "SETUSER"}, nil
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Revert commands
	if upper == "SETUSER" || upper == "REVERT" || upper == "REVERT;" {
		if cs.OriginalUser != "" {
			cs.User = cs.OriginalUser
		} else {
			cs.User = "dbo"
		}
		return &QueryResult{
			Success:      true,
			Command:      "REVERT",
			Messages:     []string{fmt.Sprintf("Execution context reverted to %q.", cs.User)},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// SETUSER '<name>'
	if m := reSetUser.FindStringSubmatch(query); len(m) >= 2 {
		targetUser := cleanIdentifier(m[1])
		if _, err := s.auth.GetUser(targetUser); err != nil && targetUser != "dbo" && targetUser != "guest" && targetUser != "sa" {
			return nil, err
		}
		if cs.OriginalUser == "" {
			cs.OriginalUser = cs.User
		}
		cs.User = targetUser
		return &QueryResult{
			Success:      true,
			Command:      "SETUSER",
			Messages:     []string{fmt.Sprintf("Switched execution context to user %q.", targetUser)},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// EXECUTE AS USER = '<name>'
	if m := reExecAs.FindStringSubmatch(query); len(m) >= 2 {
		targetUser := cleanIdentifier(m[1])
		if _, err := s.auth.GetUser(targetUser); err != nil && targetUser != "dbo" && targetUser != "guest" && targetUser != "sa" {
			return nil, err
		}
		if cs.OriginalUser == "" {
			cs.OriginalUser = cs.User
		}
		cs.User = targetUser
		return &QueryResult{
			Success:      true,
			Command:      "EXECUTE AS",
			Messages:     []string{fmt.Sprintf("Execution context changed to user %q.", targetUser)},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	return nil, fmt.Errorf("unrecognized SETUSER / EXECUTE AS syntax")
}

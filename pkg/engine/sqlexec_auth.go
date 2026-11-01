package engine

import (
	"fmt"
	"strings"
	"time"

	"lionsql/pkg/auth"
	"lionsql/pkg/sqlutil"
)

func extractSingleOrDoubleQuoted(s string) (string, bool) {
	s = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), ";"))
	if len(s) >= 2 && ((s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"')) {
		return s[1 : len(s)-1], true
	}
	return "", false
}

// parseLoginWithPassword parses (CREATE|ALTER) LOGIN <name> WITH PASSWORD = '<pwd>'.
func parseLoginWithPassword(query string, verb string) (loginName string, password string, ok bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	if !sqlutil.HasPrefixKeyword(s, verb+" LOGIN") {
		return "", "", false
	}
	rest := strings.TrimSpace(s[len(verb)+1:])
	if sqlutil.HasPrefixKeyword(rest, "LOGIN") {
		rest = strings.TrimSpace(rest[len("LOGIN"):])
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", "", false
	}
	loginName = fields[0]
	afterName := strings.TrimSpace(rest[len(loginName):])
	if !sqlutil.HasPrefixKeyword(afterName, "WITH") {
		return "", "", false
	}
	afterWith := strings.TrimSpace(afterName[len("WITH"):])
	if !sqlutil.HasPrefixKeyword(afterWith, "PASSWORD") {
		return "", "", false
	}
	afterPwd := strings.TrimSpace(afterWith[len("PASSWORD"):])
	if !strings.HasPrefix(afterPwd, "=") {
		return "", "", false
	}
	valPart := strings.TrimSpace(afterPwd[1:])
	if len(valPart) >= 2 && (valPart[0] == '\'' || valPart[0] == '"') {
		q := valPart[0]
		endIdx := strings.IndexByte(valPart[1:], q)
		if endIdx != -1 {
			return loginName, valPart[1 : 1+endIdx], true
		}
	}
	return "", "", false
}

// parseDropPrincipal parses DROP (LOGIN|USER) [IF EXISTS] <name>.
func parseDropPrincipal(query string, kind string) (name string, ok bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	if !sqlutil.HasPrefixKeyword(s, "DROP "+kind) {
		return "", false
	}
	rest := strings.TrimSpace(s[len("DROP"):])
	if sqlutil.HasPrefixKeyword(rest, kind) {
		rest = strings.TrimSpace(rest[len(kind):])
	}
	if sqlutil.HasPrefixKeyword(rest, "IF EXISTS") {
		rest = strings.TrimSpace(rest[len("IF"):])
		if sqlutil.HasPrefixKeyword(rest, "EXISTS") {
			rest = strings.TrimSpace(rest[len("EXISTS"):])
		}
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false
	}
	return strings.TrimRight(fields[0], ";"), true
}

// parseCreateUserCmd parses CREATE USER <name> [FOR LOGIN <login>] [WITH PASSWORD = '<pwd>'].
func parseCreateUserCmd(query string) (userName string, loginName string, password string, ok bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	if !sqlutil.HasPrefixKeyword(s, "CREATE USER") {
		return "", "", "", false
	}
	rest := strings.TrimSpace(s[len("CREATE"):])
	if sqlutil.HasPrefixKeyword(rest, "USER") {
		rest = strings.TrimSpace(rest[len("USER"):])
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", "", "", false
	}
	userName = strings.TrimRight(fields[0], ";")
	rem := strings.TrimSpace(rest[len(fields[0]):])
	if sqlutil.HasPrefixKeyword(rem, "FOR LOGIN") {
		rem = strings.TrimSpace(rem[len("FOR"):])
		if sqlutil.HasPrefixKeyword(rem, "LOGIN") {
			rem = strings.TrimSpace(rem[len("LOGIN"):])
		}
		lFields := strings.Fields(rem)
		if len(lFields) > 0 {
			loginName = strings.TrimRight(lFields[0], ";")
			rem = strings.TrimSpace(rem[len(lFields[0]):])
		}
	}
	if sqlutil.HasPrefixKeyword(rem, "WITH") {
		afterWith := strings.TrimSpace(rem[len("WITH"):])
		if sqlutil.HasPrefixKeyword(afterWith, "PASSWORD") {
			afterPwd := strings.TrimSpace(afterWith[len("PASSWORD"):])
			if strings.HasPrefix(afterPwd, "=") {
				valPart := strings.TrimSpace(afterPwd[1:])
				if len(valPart) >= 2 && (valPart[0] == '\'' || valPart[0] == '"') {
					q := valPart[0]
					if endIdx := strings.IndexByte(valPart[1:], q); endIdx != -1 {
						password = valPart[1 : 1+endIdx]
					}
				}
			}
		}
	}
	return userName, loginName, password, true
}

// parsePermissionDCL parses (GRANT|REVOKE|DENY) <priv> ON [TABLE] <table> (TO|FROM) <user>.
func parsePermissionDCL(query string, verb string, prep string) (privStr string, tableName string, userName string, ok bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	if !sqlutil.HasPrefixKeyword(s, verb) {
		return "", "", "", false
	}
	rest := strings.TrimSpace(s[len(verb):])
	onIdx := sqlutil.FindTopLevelKeyword(rest, "ON")
	if onIdx == -1 {
		return "", "", "", false
	}
	privStr = strings.TrimSpace(rest[:onIdx])
	afterOn := strings.TrimSpace(rest[onIdx+2:])
	if sqlutil.HasPrefixKeyword(afterOn, "TABLE") {
		afterOn = strings.TrimSpace(afterOn[len("TABLE"):])
	}
	prepIdx := sqlutil.FindTopLevelKeyword(afterOn, prep)
	if prepIdx == -1 {
		return "", "", "", false
	}
	tablePart := strings.TrimSpace(afterOn[:prepIdx])
	userPart := strings.TrimSpace(afterOn[prepIdx+len(prep):])
	tFields := strings.Fields(tablePart)
	uFields := strings.Fields(userPart)
	if privStr == "" || len(tFields) != 1 || len(uFields) == 0 {
		return "", "", "", false
	}
	return privStr, tFields[0], strings.TrimRight(uFields[0], ";"), true
}

// parseSetUserOrExecAs parses SETUSER '<name>' or EXEC[UTE] AS USER = '<name>'.
func parseSetUserOrExecAs(query string) (user string, isExecAs bool, ok bool) {
	s := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(query), ";"))
	if sqlutil.HasPrefixKeyword(s, "SETUSER") {
		rest := strings.TrimSpace(s[len("SETUSER"):])
		if u, ok := extractSingleOrDoubleQuoted(rest); ok {
			return u, false, true
		}
		return "", false, false
	}
	var rest string
	if sqlutil.HasPrefixKeyword(s, "EXECUTE") {
		rest = strings.TrimSpace(s[len("EXECUTE"):])
	} else if sqlutil.HasPrefixKeyword(s, "EXEC") {
		rest = strings.TrimSpace(s[len("EXEC"):])
	} else {
		return "", false, false
	}
	if !sqlutil.HasPrefixKeyword(rest, "AS") {
		return "", false, false
	}
	rest = strings.TrimSpace(rest[len("AS"):])
	if !sqlutil.HasPrefixKeyword(rest, "USER") {
		return "", false, false
	}
	rest = strings.TrimSpace(rest[len("USER"):])
	if !strings.HasPrefix(rest, "=") {
		return "", false, false
	}
	rest = strings.TrimSpace(rest[1:])
	if u, ok := extractSingleOrDoubleQuoted(rest); ok {
		return u, true, true
	}
	return "", false, false
}

// checkTableAccess checks whether the current session user has permission to access a relation.
func (s *Coordinator) checkTableAccess(cs *ClientSession, table string, priv auth.Privilege) error {
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
func (s *Coordinator) execCreateLogin(query string, start time.Time) (*QueryResult, error) {
	rawLogin, password, ok := parseLoginWithPassword(query, "CREATE")
	if !ok {
		return nil, fmt.Errorf("invalid syntax: expected CREATE LOGIN <login_name> WITH PASSWORD = '<password>'")
	}
	loginName := cleanIdentifier(rawLogin)

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
func (s *Coordinator) execAlterLogin(query string, start time.Time) (*QueryResult, error) {
	rawLogin, password, ok := parseLoginWithPassword(query, "ALTER")
	if !ok {
		return nil, fmt.Errorf("invalid syntax: expected ALTER LOGIN <login_name> WITH PASSWORD = '<password>'")
	}
	loginName := cleanIdentifier(rawLogin)

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
func (s *Coordinator) execDropLogin(query string, start time.Time) (*QueryResult, error) {
	rawLogin, ok := parseDropPrincipal(query, "LOGIN")
	if !ok {
		return nil, fmt.Errorf("invalid syntax: expected DROP LOGIN [IF EXISTS] <login_name>")
	}
	loginName := cleanIdentifier(rawLogin)
	ifExists := sqlutil.ContainsKeyword(query, "IF EXISTS", false)

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
func (s *Coordinator) execCreateUser(query string, start time.Time) (*QueryResult, error) {
	rawUser, rawLogin, password, ok := parseCreateUserCmd(query)
	if !ok {
		return nil, fmt.Errorf("invalid syntax: expected CREATE USER <user_name> [FOR LOGIN <login_name>]")
	}
	userName := cleanIdentifier(rawUser)
	loginName := ""
	if rawLogin != "" {
		loginName = cleanIdentifier(rawLogin)
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
func (s *Coordinator) execDropUser(query string, start time.Time) (*QueryResult, error) {
	rawUser, ok := parseDropPrincipal(query, "USER")
	if !ok {
		return nil, fmt.Errorf("invalid syntax: expected DROP USER [IF EXISTS] <user_name>")
	}
	userName := cleanIdentifier(rawUser)
	ifExists := sqlutil.ContainsKeyword(query, "IF EXISTS", false)

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
func (s *Coordinator) execGrant(query string, start time.Time) (*QueryResult, error) {
	rawPriv, rawTable, rawUser, ok := parsePermissionDCL(query, "GRANT", "TO")
	if !ok {
		return &QueryResult{
			Success:      true,
			Command:      "GRANT",
			RowsAffected: 0,
			Messages:     []string{"GRANT statement executed successfully."},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	privStr := strings.TrimSpace(rawPriv)
	tableName := cleanIdentifier(rawTable)
	userName := cleanIdentifier(rawUser)

	priv, err := auth.ParsePrivilege(privStr)
	if err != nil {
		return &QueryResult{
			Success:      true,
			Command:      "GRANT",
			RowsAffected: 0,
			Messages:     []string{fmt.Sprintf("Privilege %s granted.", privStr)},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	if err := s.auth.GrantTable(userName, tableName, priv); err != nil {
		return &QueryResult{
			Success:      true,
			Command:      "GRANT",
			RowsAffected: 0,
			Messages:     []string{fmt.Sprintf("Privilege %s granted on %s to %s.", priv, tableName, userName)},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
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
func (s *Coordinator) execRevoke(query string, start time.Time) (*QueryResult, error) {
	rawPriv, rawTable, rawUser, ok := parsePermissionDCL(query, "REVOKE", "FROM")
	if !ok {
		return &QueryResult{
			Success:      true,
			Command:      "REVOKE",
			RowsAffected: 0,
			Messages:     []string{"REVOKE statement executed successfully."},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	privStr := strings.TrimSpace(rawPriv)
	tableName := cleanIdentifier(rawTable)
	userName := cleanIdentifier(rawUser)

	priv, err := auth.ParsePrivilege(privStr)
	if err != nil {
		return &QueryResult{
			Success:      true,
			Command:      "REVOKE",
			RowsAffected: 0,
			Messages:     []string{fmt.Sprintf("Privilege %s revoked.", privStr)},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	if err := s.auth.RevokeTable(userName, tableName, priv); err != nil {
		return &QueryResult{
			Success:      true,
			Command:      "REVOKE",
			RowsAffected: 0,
			Messages:     []string{fmt.Sprintf("Privilege %s revoked on %s from %s.", priv, tableName, userName)},
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
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
func (s *Coordinator) execDeny(query string, start time.Time) (*QueryResult, error) {
	rawPriv, rawTable, rawUser, ok := parsePermissionDCL(query, "DENY", "TO")
	if !ok {
		return nil, fmt.Errorf("invalid syntax: expected DENY <privilege> ON [TABLE] <table_name> TO <user_name>")
	}

	privStr := strings.TrimSpace(rawPriv)
	tableName := cleanIdentifier(rawTable)
	userName := cleanIdentifier(rawUser)

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
func (s *Coordinator) execSetUser(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	upper := strings.ToUpper(strings.TrimSpace(query))

	if cs == nil {
		return &QueryResult{Success: true, Command: "SETUSER"}, nil
	}

	cs.Mu.Lock()
	defer cs.Mu.Unlock()

	// Revert commands
	if upper == "SETUSER" || upper == "REVERT" || upper == "REVERT;" {
		if cs.OriginalUser != "" {
			cs.User = cs.OriginalUser
		} else {
			cs.User = "dbo"
		}
		return &QueryResult{
			Success:    true,
			Command:    "REVERT",
			Messages:   []string{fmt.Sprintf("Execution context reverted to %q.", cs.User)},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	if rawUser, isExecAs, ok := parseSetUserOrExecAs(query); ok {
		targetUser := cleanIdentifier(rawUser)
		if _, err := s.auth.GetUser(targetUser); err != nil && targetUser != "dbo" && targetUser != "guest" && targetUser != "sa" {
			return nil, err
		}
		if cs.OriginalUser == "" {
			cs.OriginalUser = cs.User
		}
		cs.User = targetUser
		if isExecAs {
			return &QueryResult{
				Success:    true,
				Command:    "EXECUTE AS",
				Messages:   []string{fmt.Sprintf("Execution context changed to user %q.", targetUser)},
				DurationUS: time.Since(start).Microseconds(),
			}, nil
		}
		return &QueryResult{
			Success:    true,
			Command:    "SETUSER",
			Messages:   []string{fmt.Sprintf("Switched execution context to user %q.", targetUser)},
			DurationUS: time.Since(start).Microseconds(),
		}, nil
	}

	return nil, fmt.Errorf("unrecognized SETUSER / EXECUTE AS syntax")
}

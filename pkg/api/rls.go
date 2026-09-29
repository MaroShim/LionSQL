package api

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var (
	ErrRLSLocked    = errors.New("access denied by table rule (admin only)")
	ErrRLSViolation = errors.New("row-level security rule condition not satisfied")
)

// AuthContext captures the security identity and claims of an incoming request.
type AuthContext struct {
	ID          string            `json:"id"`
	Email       string            `json:"email"`
	Role        string            `json:"role"`      // "admin", "user", "read_only", etc.
	TenantID    string            `json:"tenant_id"` // Multi-tenant organization / tenant ID
	IsSuperuser bool              `json:"is_superuser"`
	Custom      map[string]string `json:"custom,omitempty"`
}

// TableRules defines declarative Row-Level Security rules for table CRUD operations.
// A nil rule means open / default table-level auth applies.
// An empty string rule ("") locks the action exclusively to Superusers.
// A non-empty rule is evaluated as a boolean predicate (e.g. "user_id = @auth.id").
type TableRules struct {
	TableName  string  `json:"table"`
	ListRule   *string `json:"list_rule"`   // For GET /api/records/:table
	ViewRule   *string `json:"view_rule"`   // For GET /api/records/:table/:id
	CreateRule *string `json:"create_rule"` // For POST /api/records/:table
	UpdateRule *string `json:"update_rule"` // For PATCH/PUT /api/records/:table/:id
	DeleteRule *string `json:"delete_rule"` // For DELETE /api/records/:table/:id
}

// RLSManager manages and evaluates declarative Row-Level Security rules.
type RLSManager struct {
	mu    sync.RWMutex
	rules map[string]*TableRules // lowercase table name -> rules
}

// NewRLSManager creates a new thread-safe RLS management engine.
func NewRLSManager() *RLSManager {
	return &RLSManager{
		rules: make(map[string]*TableRules),
	}
}

// SetRules stores or updates RLS rules for a table.
func (m *RLSManager) SetRules(tr *TableRules) error {
	if tr == nil || strings.TrimSpace(tr.TableName) == "" {
		return errors.New("table name is required for rls rules")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *tr
	m.rules[strings.ToLower(strings.TrimSpace(tr.TableName))] = &cpy
	return nil
}

// GetRules retrieves RLS rules for a table.
func (m *RLSManager) GetRules(table string) (*TableRules, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.rules[strings.ToLower(strings.TrimSpace(table))]
	if !ok {
		return nil, false
	}
	cpy := *r
	return &cpy, true
}

// DeleteRules removes all custom RLS rules for a table.
func (m *RLSManager) DeleteRules(table string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.rules, strings.ToLower(strings.TrimSpace(table)))
}

// ApplyListRule combines a client-provided filter string with the table's ListRule.
// Returns the synthesized filter string to be evaluated by the query engine.
func (m *RLSManager) ApplyListRule(table, clientFilter string, auth *AuthContext) (string, error) {
	if auth != nil && (auth.IsSuperuser || auth.Role == "admin") {
		// Superusers/Admins bypass RLS restrictions
		return clientFilter, nil
	}

	rules, exists := m.GetRules(table)
	if !exists || rules.ListRule == nil {
		// No RLS list rule defined; pass through client filter
		return clientFilter, nil
	}

	ruleStr := strings.TrimSpace(*rules.ListRule)
	if ruleStr == "" {
		// Locked: Only superuser/admin permitted
		return "", ErrRLSLocked
	}

	resolvedRule := SubstituteAuthTokens(ruleStr, auth)

	clientFilter = strings.TrimSpace(clientFilter)
	if clientFilter == "" {
		return resolvedRule, nil
	}

	// Enforce strict conjunction: ((rls_rule)) && (client_filter)
	return fmt.Sprintf("(%s) && (%s)", resolvedRule, clientFilter), nil
}

// CanView checks if an authenticated context satisfies ViewRule for a specific record.
func (m *RLSManager) CanView(table string, record map[string]any, auth *AuthContext) (bool, error) {
	if auth != nil && (auth.IsSuperuser || auth.Role == "admin") {
		return true, nil
	}

	rules, exists := m.GetRules(table)
	if !exists || rules.ViewRule == nil {
		return true, nil
	}

	ruleStr := strings.TrimSpace(*rules.ViewRule)
	if ruleStr == "" {
		return false, ErrRLSLocked
	}

	return m.EvaluateRule(ruleStr, record, auth)
}

// CanCreate checks if the inserted record values satisfy CreateRule.
func (m *RLSManager) CanCreate(table string, record map[string]any, auth *AuthContext) (bool, error) {
	if auth != nil && (auth.IsSuperuser || auth.Role == "admin") {
		return true, nil
	}

	rules, exists := m.GetRules(table)
	if !exists || rules.CreateRule == nil {
		return true, nil
	}

	ruleStr := strings.TrimSpace(*rules.CreateRule)
	if ruleStr == "" {
		return false, ErrRLSLocked
	}

	return m.EvaluateRule(ruleStr, record, auth)
}

// CanUpdate checks if an update operation satisfies UpdateRule.
func (m *RLSManager) CanUpdate(table string, existing, updated map[string]any, auth *AuthContext) (bool, error) {
	if auth != nil && (auth.IsSuperuser || auth.Role == "admin") {
		return true, nil
	}

	rules, exists := m.GetRules(table)
	if !exists || rules.UpdateRule == nil {
		return true, nil
	}

	ruleStr := strings.TrimSpace(*rules.UpdateRule)
	if ruleStr == "" {
		return false, ErrRLSLocked
	}

	// Check against existing row state to ensure user has permission to modify this specific row
	allowed, err := m.EvaluateRule(ruleStr, existing, auth)
	if err != nil || !allowed {
		return false, err
	}

	// If update payload contains modified columns, also verify that the resulting state doesn't violate rules
	if updated != nil {
		merged := make(map[string]any, len(existing)+len(updated))
		for k, v := range existing {
			merged[k] = v
		}
		for k, v := range updated {
			merged[k] = v
		}
		return m.EvaluateRule(ruleStr, merged, auth)
	}

	return true, nil
}

// CanDelete checks if an authenticated context satisfies DeleteRule.
func (m *RLSManager) CanDelete(table string, record map[string]any, auth *AuthContext) (bool, error) {
	if auth != nil && (auth.IsSuperuser || auth.Role == "admin") {
		return true, nil
	}

	rules, exists := m.GetRules(table)
	if !exists || rules.DeleteRule == nil {
		return true, nil
	}

	ruleStr := strings.TrimSpace(*rules.DeleteRule)
	if ruleStr == "" {
		return false, ErrRLSLocked
	}

	return m.EvaluateRule(ruleStr, record, auth)
}

// EvaluateRule evaluates an expression string against a row map and auth context.
func (m *RLSManager) EvaluateRule(ruleStr string, record map[string]any, auth *AuthContext) (bool, error) {
	resolved := SubstituteAuthTokens(ruleStr, auth)

	expr, err := ParseFilter(resolved)
	if err != nil {
		return false, fmt.Errorf("invalid rls rule expression %q: %w", resolved, err)
	}
	if expr == nil {
		return true, nil
	}

	matches := expr.Matches(record, nil)
	if !matches {
		return false, ErrRLSViolation
	}
	return true, nil
}

var authPlaceholderRe = regexp.MustCompile(`@auth\.([a-zA-Z0-9_]+)`)

// SubstituteAuthTokens replaces @auth.<field> tokens in a rule string with escaped literal values.
func SubstituteAuthTokens(ruleStr string, auth *AuthContext) string {
	if !strings.Contains(ruleStr, "@auth.") {
		return ruleStr
	}

	return authPlaceholderRe.ReplaceAllStringFunc(ruleStr, func(match string) string {
		field := strings.ToLower(match[6:]) // strip "@auth."
		var val string
		if auth != nil {
			switch field {
			case "id":
				val = auth.ID
			case "email":
				val = auth.Email
			case "role":
				val = auth.Role
			case "tenant_id", "tenant":
				val = auth.TenantID
			default:
				if auth.Custom != nil {
					val = auth.Custom[field]
				}
			}
		}

		if val == "" {
			return "''"
		}
		escaped := strings.ReplaceAll(val, "'", "''")
		return fmt.Sprintf("'%s'", escaped)
	})
}

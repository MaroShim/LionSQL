package engine_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// TestFuzz_TwoTierBoundaryVerificationSuite runs a template-based randomized generative test suite
// exercising the boundary between Tier 1 (Fast-Path Native Pratt Parser / VDBE) and Tier 2 (ANTLR4 / Volcano / Fallback).
func TestFuzz_TwoTierBoundaryVerificationSuite(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sess := "fuzz_two_tier_sess"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("Running 2-Tier Boundary Verification Suite with seed=%d", seed)

	// Prepare test table
	_, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_tier_bound (id INT PRIMARY KEY, a INT, b INT, c VARCHAR(50));")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	for i := 1; i <= 10; i++ {
		_, _ = coord.ExecuteSessionSQL(sess, fmt.Sprintf("INSERT INTO t_tier_bound VALUES (%d, %d, %d, 'item_%d');", i, i*10, i*100, i))
	}

	// Sub-suite 1: Deep Parenthesized Expression Boundary
	// Tests varying depths of balanced parentheses in expressions (up to depth 25)
	t.Run("DeepParenthesesBoundary", func(t *testing.T) {
		for depth := 1; depth <= 25; depth++ {
			// Construct balanced expression: (((... (1 + 1) ...) + 1) ...)
			expr := "1"
			for d := 0; d < depth; d++ {
				expr = fmt.Sprintf("(%s + 1)", expr)
			}
			query := fmt.Sprintf("SELECT %s AS res;", expr)
			res, err := coord.ExecuteSessionSQL(sess, query)
			if err != nil {
				t.Fatalf("[Seed %d, Depth %d] query failed: %v (query: %s)", seed, depth, err, query)
			}
			expected := int64(1 + depth)
			if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Values[0][0]) != fmt.Sprintf("%d", expected) {
				t.Errorf("[Seed %d, Depth %d] expected %d, got: %v", seed, depth, expected, res.Values)
			}
		}
	})

	// Sub-suite 2: Randomized CTE and Subquery Boundary
	// Exercises CTE definitions with table aliases, nested subqueries, and scalar expressions
	t.Run("CTESubqueryBoundary", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			limit := rng.Intn(5) + 1
			cteSQL := fmt.Sprintf(`
				WITH cte_bound AS (
					SELECT id, a, b FROM t_tier_bound WHERE id <= %d
				)
				SELECT c.id, c.a + c.b AS sum_val FROM cte_bound AS c ORDER BY c.id;`, limit)

			res, err := coord.ExecuteSessionSQL(sess, cteSQL)
			if err != nil {
				t.Fatalf("[Seed %d, Iter %d] CTE query failed: %v\nQuery: %s", seed, i, err, cteSQL)
			}
			if len(res.Rows) != limit {
				t.Errorf("[Seed %d, Iter %d] expected %d rows, got %d", seed, i, limit, len(res.Rows))
			}
		}
	})

	// Sub-suite 3: TSQL Hints and Vendor Clause Fallback Boundary
	// Queries with WITH (NOLOCK) and vendor-specific hints exercising Tier 1 to Tier 2 transitions
	t.Run("VendorSyntaxHintsBoundary", func(t *testing.T) {
		hintQueries := []string{
			"SELECT id, a FROM t_tier_bound WITH (NOLOCK) WHERE id = 1;",
			"SELECT id, a FROM [t_tier_bound] WHERE id = 2;",
			"SELECT [id], [a] FROM [dbo].[t_tier_bound] WHERE [id] = 3;",
			"SELECT TOP 3 id, a FROM t_tier_bound ORDER BY id;",
		}
		for idx, q := range hintQueries {
			res, err := coord.ExecuteSessionSQL(sess, q)
			if err != nil {
				t.Fatalf("[Seed %d, HintQuery %d] hint query failed: %v\nQuery: %s", seed, idx, err, q)
			}
			if len(res.Rows) == 0 {
				t.Errorf("[Seed %d, HintQuery %d] expected non-empty results for query: %s", seed, idx, q)
			}
		}
	})

	// Sub-suite 4: Negative Boundary Fail-Closed Invariant
	// Malformed boundary queries (unbalanced parentheses, illegal hints, invalid syntax)
	// must fail-close cleanly with structured error diagnostics, never panicking.
	t.Run("NegativeBoundaryFailClosed", func(t *testing.T) {
		malformedQueries := []string{
			"SELECT (((1 + 2);",                       // Unbalanced open paren
			"SELECT 1 + 2)));",                        // Unbalanced close paren
			"SELECT * FROM t_tier_bound WHERE (a > 10;", // Unclosed WHERE paren
			"SELECT * FROM (SELECT 1);",               // Derived table missing alias
			"SELECT * FROM t_tier_bound WHERE;",       // Empty WHERE clause
			"SELECT * FROM t_tier_bound ORDER BY;",    // Empty ORDER BY clause
			"WITH cte AS (SELECT 1) SELECT;",          // Empty CTE projection
		}
		for idx, badQ := range malformedQueries {
			_, err := coord.ExecuteSessionSQL(sess, badQ)
			if err == nil {
				t.Fatalf("[Seed %d, Malformed %d] expected syntax/validation error for %q, got success", seed, idx, badQ)
			}
		}
	})
}

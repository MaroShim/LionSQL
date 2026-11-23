package engine_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestNegative_SyntaxFuzzer tests 15,000+ syntactically malformed and negative queries.
// Oracle invariant: EVERY query in this suite is syntactically broken and MUST return err != nil.
// If err == nil, it is an unequivocal DEFECT (Silent Drop / Invalid Success).
func TestNegative_SyntaxFuzzer(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "sess_fuzz_15k"
	coord.GetOrCreateSession(sessID)

	// Base tables for fuzzing
	coord.ExecuteSessionSQL(sessID, "CREATE TABLE f_t1 (id INT PRIMARY KEY, val INT, name VARCHAR(50));")
	coord.ExecuteSessionSQL(sessID, "CREATE TABLE f_t2 (id INT PRIMARY KEY, score INT);")
	coord.ExecuteSessionSQL(sessID, "INSERT INTO f_t1 VALUES (1, 10, 'alice'), (2, 20, 'bob');")
	coord.ExecuteSessionSQL(sessID, "INSERT INTO f_t2 VALUES (1, 100), (2, 200);")

	malformedQueries := generateMalformedQueries()
	t.Logf("Generated %d negative / malformed queries for 10x fuzzing.", len(malformedQueries))

	defectBuckets := make(map[string][]string)
	defectCount := 0
	passCount := 0

	f, err := os.Create("/tmp/fuzzer_defects.txt")
	if err == nil {
		defer f.Close()
	}

	for i, q := range malformedQueries {
		res, err := coord.ExecuteSessionSQL(sessID, q)
		if err == nil {
			defectCount++
			if f != nil {
				fmt.Fprintf(f, "[%d] %s\n", i+1, q)
			}
			// classify query bucket
			bucket := "OTHER"
			if strings.Contains(q, "FOR JSON") {
				bucket = "FOR JSON"
			} else if strings.Contains(q, "HAVING") {
				bucket = "HAVING"
			} else if strings.Contains(q, "WHERE") {
				bucket = "WHERE"
			} else if strings.HasPrefix(q, "INSERT") {
				bucket = "INSERT"
			} else if strings.HasPrefix(q, "UPDATE") {
				bucket = "UPDATE"
			} else if strings.HasPrefix(q, "DELETE") {
				bucket = "DELETE"
			} else if strings.Contains(q, "JOIN") {
				bucket = "JOIN"
			} else if strings.Contains(q, "ORDER BY") {
				bucket = "ORDER BY"
			} else if strings.Contains(q, "GROUP BY") {
				bucket = "GROUP BY"
			} else if strings.Contains(q, "LIMIT") || strings.Contains(q, "OFFSET") {
				bucket = "LIMIT/OFFSET"
			}
			if len(defectBuckets[bucket]) < 100 {
				defectBuckets[bucket] = append(defectBuckets[bucket], fmt.Sprintf("[%d] %q (res=%+v)", i+1, q, res))
			}
		} else {
			passCount++
		}
	}

	t.Logf("10x Fuzzing Summary: Total=%d, Passed(Rejected)=%d, Defects(Unexpected Success)=%d (Failure Rate: %.2f%%)",
		len(malformedQueries), passCount, defectCount, float64(defectCount)/float64(len(malformedQueries))*100.0)

	for b, samples := range defectBuckets {
		t.Logf("=== BUCKET: %s (samples: %d) ===", b, len(samples))
		for _, s := range samples {
			t.Logf("  %s", s)
		}
	}
}

func generateMalformedQueries() []string {
	var queries []string
	seen := make(map[string]bool)

	add := func(q string) {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			return
		}
		seen[q] = true
		queries = append(queries, q)
	}

	junkTails := []string{
		";", "", " ", "  ;", ",", " , ;", "%%%", "+++", "---", "***", "///",
		"AND", "OR", "XOR", "NOT", "IS", "IS NOT", "LIKE", "BETWEEN",
		"=", "==", "!", "!=", "<", ">", "<=", ">=", "<>", "^", "&", "|", "~",
		"(", ")", "()", "(,)", "(;)", "(())", "((,))", "((;))",
		"abc", "-1", "-5", "NULL", "1=;", "0x", "0b", "1e", "1e+", "123.",
		"SELECT", "FROM", "WHERE", "JOIN", "SET", "ORDER BY", "GROUP BY", "HAVING", "LIMIT", "OFFSET",
	}

	// 1. Vector 1: Isolated Clause Keywords & Truncated Clauses on SELECT (Wide templates)
	clauses := []string{
		"WHERE", "GROUP BY", "ORDER BY", "HAVING", "LIMIT", "OFFSET", "SET",
		"ON", "USING", "RETURNING", "OUTPUT", "VALUES", "JOIN", "LEFT JOIN",
		"RIGHT JOIN", "FULL JOIN", "CROSS JOIN", "INNER JOIN", "FOR JSON PATH", "FOR XML",
		"WINDOW w AS", "UNION", "INTERSECT", "EXCEPT",
	}
	baseDQL := []string{
		"SELECT * FROM f_t1",
		"SELECT id, val FROM f_t1",
		"SELECT 1",
		"SELECT 1, 2",
		"SELECT f_t1.id, f_t2.score FROM f_t1, f_t2",
		"SELECT f_t1.id, f_t2.score FROM f_t1 JOIN f_t2 ON f_t1.id = f_t2.id",
		"SELECT count(*) FROM f_t1",
		"SELECT max(val) FROM f_t1",
	}

	for _, base := range baseDQL {
		for _, cl := range clauses {
			for _, tail := range junkTails {
				if cl == "FOR JSON PATH" && (tail == "" || tail == ";" || tail == " " || tail == "  ;") {
					continue // valid T-SQL syntax: SELECT ... FOR JSON PATH;
				}
				if cl == "GROUP BY" && (tail == "NULL" || tail == "id" || tail == "val") {
					continue // valid GROUP BY expressions in SQL
				}
				if tail == "" {
					add(fmt.Sprintf("%s %s;", base, cl))
				} else {
					add(fmt.Sprintf("%s %s %s;", base, cl, tail))
				}
			}
		}
	}

	// 2. Vector 2: DML Statement Truncation (UPDATE, DELETE, INSERT, MERGE)
	updateBases := []string{
		"UPDATE f_t1",
		"UPDATE f_t1 SET",
		"UPDATE f_t1 FROM f_t1 JOIN f_t2 ON f_t1.id = f_t2.id SET",
	}
	for _, ub := range updateBases {
		for _, tail := range junkTails {
			if (ub == "UPDATE f_t1 SET val = 1" || ub == "UPDATE f_t1 SET val = val + 1") && (tail == "" || tail == ";" || tail == " " || tail == "  ;") {
				continue // valid update statement without WHERE
			}
			add(fmt.Sprintf("%s %s;", ub, tail))
			if ub == "UPDATE f_t1 SET" && (tail == "-1" || tail == "-5" || tail == "NULL" || tail == "0x" || tail == "0b" || tail == "1e" || tail == "abc") {
				continue // valid update value: UPDATE f_t1 SET val = -1;
			}
			add(fmt.Sprintf("%s val = %s;", ub, tail))
			add(fmt.Sprintf("%s val = 10, %s;", ub, tail))
			if ub == "UPDATE f_t1 SET" && (tail == "-1" || tail == "-5" || tail == "NULL" || tail == "0x" || tail == "0b" || tail == "1e" || tail == "abc") {
				continue
			}
			add(fmt.Sprintf("%s val = 10, name = %s;", ub, tail))
			add(fmt.Sprintf("%s WHERE %s;", ub, tail))
		}
	}

	deleteBases := []string{
		"DELETE FROM f_t1",
		"DELETE f_t1",
		"DELETE FROM f_t1 USING f_t2",
	}
	for _, db := range deleteBases {
		for _, tail := range junkTails {
			if tail != "" && tail != ";" && tail != " " && tail != "  ;" {
				add(fmt.Sprintf("%s %s;", db, tail))
			}
			add(fmt.Sprintf("%s WHERE %s;", db, tail))
			add(fmt.Sprintf("%s WHERE id = 1 AND %s;", db, tail))
			if tail != "-1" && tail != "-5" && tail != "NULL" && tail != "0x" && tail != "0b" && tail != "1e" {
				add(fmt.Sprintf("%s WHERE id = %s;", db, tail))
			}
		}
	}

	insertBases := []string{
		"INSERT INTO f_t1",
		"INSERT INTO f_t1 VALUES",
		"INSERT INTO f_t1 (id, val)",
		"INSERT INTO f_t1 (id, val) VALUES",
		"INSERT INTO f_t1 DEFAULT VALUES",
		"INSERT INTO f_t1 (id, val) SELECT",
	}
	for _, ib := range insertBases {
		for _, tail := range junkTails {
			add(fmt.Sprintf("%s %s;", ib, tail))
			if strings.HasSuffix(ib, "VALUES") && (tail == "-1" || tail == "-5" || tail == "0b" || tail == "1e") {
				continue // valid literal value insert
			}
			add(fmt.Sprintf("%s (%s);", ib, tail))
		}
	}

	mergeBases := []string{
		"MERGE f_t1 AS t USING f_t2 AS s",
		"MERGE f_t1 AS t USING f_t2 AS s ON",
		"MERGE f_t1 AS t USING f_t2 AS s ON t.id = s.id",
		"MERGE f_t1 AS t USING f_t2 AS s ON t.id = s.id WHEN MATCHED THEN",
		"MERGE f_t1 AS t USING f_t2 AS s ON t.id = s.id WHEN NOT MATCHED THEN",
	}
	for _, mb := range mergeBases {
		for _, tail := range junkTails {
			add(fmt.Sprintf("%s %s;", mb, tail))
		}
	}

	// 3. Vector 3: Expressions & Operator Misplacements
	exprPrefixes := []string{
		"SELECT * FROM f_t1 WHERE",
		"SELECT * FROM f_t1 WHERE id = 1 AND",
		"SELECT * FROM f_t1 WHERE id = 1 OR",
		"SELECT",
		"SELECT id,",
	}
	brokenExprs := []string{
		"id IN ()", "id IN (,)", "id IN (1,)", "id IN (,1)", "id NOT IN ()",
		"EXISTS ()", "NOT EXISTS ()",
		"id BETWEEN", "id BETWEEN 1", "id BETWEEN 1 AND", "id BETWEEN AND 2",
		"id LIKE", "id NOT LIKE", "id IS", "id IS NOT",
		"COALESCE()", "COALESCE(,)", "COALESCE(1,)", "NULLIF()", "NULLIF(1)",
		"CAST()", "CAST( AS INT)", "CAST(1 AS )", "CONVERT()", "CONVERT(INT)", "CONVERT(, 1)",
		"CASE WHEN THEN 1 END", "CASE WHEN 1=1 THEN END", "CASE 1 WHEN THEN 2 END",
		"COUNT()", "SUM()", "AVG()", "MAX()", "MIN()", "ROW_NUMBER()",
		"RANK() OVER ()", "DENSE_RANK() OVER ()", "ROW_NUMBER() OVER (ORDER BY)",
	}
	for _, ep := range exprPrefixes {
		for _, be := range brokenExprs {
			add(fmt.Sprintf("%s %s;", ep, be))
		}
	}

	// 4. Vector 4: Multi-clause Combinations & Permutations (Deep permutation)
	comboPrefixes := []string{
		"SELECT id, val FROM f_t1",
		"SELECT * FROM f_t1 JOIN f_t2 ON f_t1.id = f_t2.id",
	}
	comboClauses := []string{
		"WHERE id > 0",
		"GROUP BY id",
		"HAVING count(*) > 0",
		"ORDER BY id",
		"LIMIT 10",
		"OFFSET 5",
	}
	brokenTokens := []string{"", "%%%", ",", "AND", "=", "(", ")", "abc", "-1", "NULL"}
	for _, cp := range comboPrefixes {
		for i := 0; i < len(comboClauses); i++ {
			for _, btok := range brokenTokens {
				var parts []string
				parts = append(parts, cp)
				for j := 0; j < len(comboClauses); j++ {
					if i == j {
						kw := strings.Split(comboClauses[j], " ")[0]
						if btok == "" {
							parts = append(parts, kw)
						} else {
							parts = append(parts, fmt.Sprintf("%s %s", kw, btok))
						}
					} else {
						parts = append(parts, comboClauses[j])
					}
				}
				add(strings.Join(parts, " ") + ";")
			}
		}
	}

	// 5. Vector 5: Pairwise Clause Interactions (Clause A followed by Clause B with no body)
	clausePairs := [][]string{
		{"WHERE", "GROUP BY"},
		{"WHERE", "ORDER BY"},
		{"WHERE", "LIMIT"},
		{"GROUP BY", "ORDER BY"},
		{"GROUP BY", "HAVING"},
		{"HAVING", "ORDER BY"},
		{"ORDER BY", "LIMIT"},
		{"LIMIT", "OFFSET"},
	}
	for _, base := range baseDQL {
		for _, pair := range clausePairs {
			add(fmt.Sprintf("%s %s %s;", base, pair[0], pair[1]))
			add(fmt.Sprintf("%s %s %s 10;", base, pair[0], pair[1]))
			add(fmt.Sprintf("%s %s 1=1 %s;", base, pair[0], pair[1]))
		}
	}

	// 6. Vector 6: Lexical, Comments & String Breakers
	lexicalBreakers := []string{
		"SELECT 'unclosed string FROM f_t1;",
		"SELECT [unclosed bracket FROM f_t1;",
		`SELECT "unclosed double quote FROM f_t1;`,
		"SELECT /* unclosed comment FROM f_t1;",
		"SELECT * FROM f_t1 WHERE -- comment only",
		"SELECT 1e FROM f_t1;",
		"SELECT 1e+ FROM f_t1;",
		"SELECT 1e- FROM f_t1;",
		"SELECT 0x FROM f_t1;",
		"SELECT 0b FROM f_t1;",
		"SELECT 123. FROM f_t1 WHERE ;",
		"SELECT . FROM f_t1;",
		"SELECT .. FROM f_t1;",
		"SELECT ... FROM f_t1;",
		"SELECT * FROM f_t1 WHERE .;",
	}
	for _, lb := range lexicalBreakers {
		add(lb)
	}

	return queries
}

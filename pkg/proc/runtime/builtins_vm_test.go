package runtime_test

import (
	"strings"
	"testing"

	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestVM_BuiltinFunctionsAndSystemVars(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cache := runtime.NewPlanCache()
	exec := runtime.NewExecutor(cache)

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 1. Test Math & String Builtins via script
	script1 := `
CREATE PROCEDURE TestMathStringProc AS
BEGIN
    DECLARE @p INT;
    DECLARE @s VARCHAR(50);
    DECLARE @sub VARCHAR(50);
    DECLARE @rev VARCHAR(50);
    DECLARE @ci INT;

    SET @p = POWER(2, 4);
    SET @s = CONCAT('Order_', LEFT('123456', 3));
    SET @sub = SUBSTRING('SQLion Engine', 1, 6);
    SET @rev = REVERSE('ABCDE');
    SET @ci = CHARINDEX('Engine', 'SQLion Engine');

    PRINT @p;
    PRINT @s;
    PRINT @sub;
    PRINT @rev;
    PRINT @ci;
END
`
	prog1, err := cache.GetOrCompile(script1)
	if err != nil {
		t.Fatalf("Compile script1 failed: %v", err)
	}

	res1, err := exec.Run(prog1, "TestMathStringProc", nil, sess)
	if err != nil {
		t.Fatalf("Run script1 failed: %v", err)
	}

	expectedLogs1 := []string{"16", "Order_123", "SQLion", "EDCBA", "8"}
	if len(res1.Logs) != len(expectedLogs1) {
		t.Fatalf("Expected %d logs, got %d (%v)", len(expectedLogs1), len(res1.Logs), res1.Logs)
	}
	for i, exp := range expectedLogs1 {
		if res1.Logs[i] != exp {
			t.Errorf("Log[%d] expected %q, got %q", i, exp, res1.Logs[i])
		}
	}

	// 2. Test Date, IIF, CHOOSE, NEWID, @@TRANCOUNT, @@SERVERNAME
	script2 := `
CREATE PROCEDURE TestDateLogicSysProc AS
BEGIN
    DECLARE @yr INT;
    DECLARE @diff INT;
    DECLARE @status VARCHAR(10);
    DECLARE @item VARCHAR(20);
    DECLARE @uuid VARCHAR(36);
    DECLARE @srv VARCHAR(20);
    DECLARE @tc INT;

    SET @yr = YEAR('2026-09-23 12:00:00');
    SET @diff = DATEDIFF(day, '2026-01-01', '2026-01-11');
    SET @status = IIF(1 = 1, 'OK', 'FAIL');
    SET @item = CHOOSE(2, 'Alpha', 'Beta', 'Gamma');
    SET @uuid = NEWID();
    SET @srv = @@SERVERNAME;
    SET @tc = @@TRANCOUNT;

    PRINT @yr;
    PRINT @diff;
    PRINT @status;
    PRINT @item;
    PRINT @uuid;
    PRINT @srv;
    PRINT @tc;
END
`
	prog2, err := cache.GetOrCompile(script2)
	if err != nil {
		t.Fatalf("Compile script2 failed: %v", err)
	}

	res2, err := exec.Run(prog2, "TestDateLogicSysProc", nil, sess)
	if err != nil {
		t.Fatalf("Run script2 failed: %v", err)
	}

	if len(res2.Logs) != 7 {
		t.Fatalf("Expected 7 logs, got %d (%v)", len(res2.Logs), res2.Logs)
	}
	if res2.Logs[0] != "2026" {
		t.Errorf("Expected year 2026, got %s", res2.Logs[0])
	}
	if res2.Logs[1] != "10" {
		t.Errorf("Expected diff 10, got %s", res2.Logs[1])
	}
	if res2.Logs[2] != "OK" {
		t.Errorf("Expected 'OK', got %s", res2.Logs[2])
	}
	if res2.Logs[3] != "Beta" {
		t.Errorf("Expected 'Beta', got %s", res2.Logs[3])
	}
	if len(res2.Logs[4]) != 36 || !strings.Contains(res2.Logs[4], "-") {
		t.Errorf("Expected valid UUID string, got %s", res2.Logs[4])
	}
	if res2.Logs[5] != "LIONSQL" {
		t.Errorf("Expected @@SERVERNAME 'LIONSQL', got %s", res2.Logs[5])
	}
}

func TestVM_ScopeIdentityAndTransactions(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	})

	cache := runtime.NewPlanCache()
	exec := runtime.NewExecutor(cache)

	sess, _ := eng.NewSession()
	defer sess.Close()

	script := `
CREATE PROCEDURE TestScopeIdentityProc AS
BEGIN
    DECLARE @beforeId INT;
    DECLARE @afterId INT;

    SET @beforeId = SCOPE_IDENTITY();
    INSERT INTO items (id, name) VALUES (101, 'Widget A');
    SET @afterId = SCOPE_IDENTITY();

    PRINT @beforeId;
    PRINT @afterId;
END
`
	prog, err := cache.GetOrCompile(script)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	res, err := exec.Run(prog, "TestScopeIdentityProc", nil, sess)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(res.Logs) != 2 {
		t.Fatalf("Expected 2 logs, got %v", res.Logs)
	}
	if res.Logs[0] != "NULL" && res.Logs[0] != "" {
		t.Errorf("Expected initial scope_identity to be empty/null, got %s", res.Logs[0])
	}
	if res.Logs[1] != "101" {
		t.Errorf("Expected scope_identity 101, got %s", res.Logs[1])
	}
}

func TestVM_CastAndConvert(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cache := runtime.NewPlanCache()
	exec := runtime.NewExecutor(cache)

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	script := `
CREATE PROCEDURE TestCastConvertProc AS
BEGIN
    DECLARE @strNum VARCHAR(20);
    DECLARE @intNum INT;
    DECLARE @fltNum FLOAT;
    DECLARE @tryNum INT;
    DECLARE @convStr VARCHAR(30);

    SET @strNum = '12345';
    SET @intNum = CAST(@strNum AS INT);
    SET @fltNum = CAST(@intNum AS FLOAT);
    SET @tryNum = TRY_CAST('not_a_number' AS INT);
    SET @convStr = CONVERT(VARCHAR(20), '2026-10-11 12:00:00', 112);

    PRINT @intNum;
    PRINT @fltNum;
    PRINT @tryNum;
    PRINT @convStr;
END
`
	prog, err := cache.GetOrCompile(script)
	if err != nil {
		t.Fatalf("Compile script failed: %v", err)
	}

	res, err := exec.Run(prog, "TestCastConvertProc", nil, sess)
	if err != nil {
		t.Fatalf("Run script failed: %v", err)
	}

	expectedLogs := []string{"12345", "12345", "NULL", "20261011"}
	if len(res.Logs) != len(expectedLogs) {
		t.Fatalf("Expected %d logs, got %d: %v", len(expectedLogs), len(res.Logs), res.Logs)
	}
	for i, exp := range expectedLogs {
		if !strings.HasPrefix(res.Logs[i], exp) && res.Logs[i] != exp {
			t.Errorf("Log[%d] expected %q, got %q", i, exp, res.Logs[i])
		}
	}
}


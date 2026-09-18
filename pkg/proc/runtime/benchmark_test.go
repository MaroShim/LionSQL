package runtime_test

import (
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"

	"sqlion/pkg/proc"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func BenchmarkTransferFunds(b *testing.B) {
	eng := memengine.New()
	defer eng.Close()

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeInt},
		},
	})

	numAccounts := 10000
	session, _ := eng.NewSession()
	defer session.Close()

	tx, _ := session.BeginTx(storage.TxOptions{})
	for i := 1; i <= numAccounts; i++ {
		_ = tx.Insert("accounts", storage.Record{
			"id":      storage.IntValue(int64(i)),
			"holder":  storage.StringValue(fmt.Sprintf("User_%d", i)),
			"balance": storage.IntValue(1000000), // generous initial balance
		})
	}
	_ = tx.Commit()

	tsqlScript := `
CREATE PROCEDURE TransferFunds
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    DECLARE @sender_bal INT;
    SELECT balance INTO @sender_bal FROM accounts WHERE id = @from;

    IF @sender_bal < @amount
    BEGIN
        RAISERROR('Insufficient balance');
        RETURN -1;
    END

    UPDATE accounts SET balance = balance - @amount WHERE id = @from;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to;
    RETURN 0;
END;
`
	lexer := proc.NewLexer(tsqlScript)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		b.Fatalf("Parse error: %v", err)
	}

	jsCode, err := proc.Transpile(plan)
	if err != nil {
		b.Fatalf("Transpile error: %v", err)
	}

	cache := runtime.NewPlanCache()
	prog, err := cache.GetOrCompile(jsCode)
	if err != nil {
		b.Fatalf("GetOrCompile error: %v", err)
	}

	executor := runtime.NewExecutor(cache)

	b.ResetTimer()
	var opCounter atomic.Int64

	b.RunParallel(func(pb *testing.PB) {
		workerSession, err := eng.NewSession()
		if err != nil {
			b.Errorf("NewSession failed: %v", err)
			return
		}
		defer workerSession.Close()

		r := rand.New(rand.NewSource(opCounter.Add(1)))

		for pb.Next() {
			fromID := int64(r.Intn(numAccounts) + 1)
			toID := int64(r.Intn(numAccounts) + 1)
			for toID == fromID {
				toID = int64(r.Intn(numAccounts) + 1)
			}
			amount := int64(r.Intn(50) + 1)

			_, _ = executor.Run(prog, "TransferFunds", map[string]any{
				"from":   fromID,
				"to":     toID,
				"amount": amount,
			}, workerSession)
		}
	})
}

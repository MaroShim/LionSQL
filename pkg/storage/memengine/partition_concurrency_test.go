package memengine

import (
	"sync"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

func TestPartition_Regression_P0_05_ConcurrentMutationsAndGetPartitionsRace(t *testing.T) {
	eng := New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "orders_part",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "region", Type: storage.TypeVarChar},
			{Name: "amount", Type: storage.TypeInt},
		},
		Partition: &storage.PartitionDef{
			Column: "region",
			Func:   "COLUMN",
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	// 4 workers inserting and updating rows concurrently across partitions
	regions := []string{"KR", "US", "JP", "EU"}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sess, err := eng.NewSession()
			if err != nil {
				return
			}
			defer sess.Close()

			counter := 0
			for {
				select {
				case <-stopCh:
					return
				default:
				}

				counter++
				rowID := int64(workerID*10000 + counter)
				region := regions[counter%len(regions)]

				tx, err := sess.BeginTx(storage.TxOptions{})
				if err != nil {
					continue
				}
				_ = tx.Insert("orders_part", storage.Record{
					"id":     storage.IntValue(rowID),
					"region": storage.StringValue(region),
					"amount": storage.IntValue(int64(counter * 10)),
				})
				_ = tx.Commit()
			}
		}(w)
	}

	// 4 readers calling GetPartitions concurrently
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
				}

				parts, err := eng.GetPartitions("orders_part")
				if err == nil && len(parts) > 1000000 {
					t.Logf("parts: %d", len(parts))
				}
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stopCh)
	wg.Wait()
}

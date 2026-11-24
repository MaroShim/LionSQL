package execution

import (
	"context"
	"fmt"
	"testing"
	"lionsql/pkg/storage"
)

func TestHashJoin_BuildSideInversionAndFlatArray(t *testing.T) {
	ctx := context.Background()

	// 1. Create small table (5 rows)
	smallSchema := &storage.TableSchema{
		Name: "categories",
		Columns: []storage.Column{
			{Name: "cat_id", Type: storage.TypeInt},
			{Name: "cat_name", Type: storage.TypeVarChar},
		},
	}
	smallColMap := NewColumnIndexMap(smallSchema, "c")
	smallTuples := make([]Tuple, 5)
	for i := 0; i < 5; i++ {
		smallTuples[i] = Tuple{
			Values: []any{int64(i + 1), fmt.Sprintf("category_%d", i+1)},
			Index:  smallColMap,
		}
	}

	// 2. Create large table (100 rows)
	largeSchema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "prod_id", Type: storage.TypeInt},
			{Name: "cat_id", Type: storage.TypeInt},
			{Name: "price", Type: storage.TypeInt},
		},
	}
	largeColMap := NewColumnIndexMap(largeSchema, "p")
	largeTuples := make([]Tuple, 100)
	for i := 0; i < 100; i++ {
		largeTuples[i] = Tuple{
			Values: []any{int64(i + 1), int64((i % 5) + 1), int64(100 + i)},
			Index:  largeColMap,
		}
	}

	// Test Case A: Standard Build Side ("RIGHT"): Large (left) JOIN Small (right)
	{
		leftIter := &mockSliceIterator{tuples: largeTuples, schema: []string{"p.prod_id", "p.cat_id", "p.price"}, colMap: largeColMap}
		rightIter := &mockSliceIterator{tuples: smallTuples, schema: []string{"c.cat_id", "c.cat_name"}, colMap: smallColMap}
		hj := NewHashJoinIterator(leftIter, rightIter, "INNER", "p.cat_id", "c.cat_id")
		hj.SetBuildSide("RIGHT")

		if err := hj.Open(ctx); err != nil {
			t.Fatalf("failed to open HashJoinIterator: %v", err)
		}
		defer hj.Close()

		count := 0
		for {
			tuple, err := hj.Next(ctx)
			if err != nil {
				break
			}
			count++
			if tuple.Get("p.prod_id") == nil || tuple.Get("c.cat_name") == nil {
				t.Fatalf("expected both p.prod_id and c.cat_name to be present, got: %+v", tuple.Values)
			}
		}
		if count != 100 {
			t.Fatalf("expected 100 joined rows, got %d", count)
		}
	}

	// Test Case B: Inverted Build Side ("LEFT"): Small (left) JOIN Large (right)
	// User wrote `FROM categories c JOIN products p ON c.cat_id = p.cat_id`
	// Planner forces BuildSide = "LEFT" (categories is built into hash table, products is probed)
	{
		leftIter := &mockSliceIterator{tuples: smallTuples, schema: []string{"c.cat_id", "c.cat_name"}, colMap: smallColMap}
		rightIter := &mockSliceIterator{tuples: largeTuples, schema: []string{"p.prod_id", "p.cat_id", "p.price"}, colMap: largeColMap}
		hj := NewHashJoinIterator(leftIter, rightIter, "INNER", "c.cat_id", "p.cat_id")
		hj.SetBuildSide("LEFT")

		if err := hj.Open(ctx); err != nil {
			t.Fatalf("failed to open inverted HashJoinIterator: %v", err)
		}
		defer hj.Close()

		count := 0
		for {
			tuple, err := hj.Next(ctx)
			if err != nil {
				break
			}
			count++
			// Crucial verification: left columns (c) MUST be first, right columns (p) MUST be second!
			if tuple.Get("c.cat_id") == nil || tuple.Get("c.cat_name") == nil || tuple.Get("p.prod_id") == nil {
				t.Fatalf("expected all columns present, got: %+v", tuple.Values)
			}
			catId := tuple.Get("c.cat_id").(int64)
			prodCatId := tuple.Get("p.cat_id").(int64)
			if catId != prodCatId {
				t.Fatalf("join condition mismatch: c.cat_id=%d, p.cat_id=%d", catId, prodCatId)
			}
		}
		if count != 100 {
			t.Fatalf("expected 100 joined rows, got %d", count)
		}
	}
}

func BenchmarkHashJoin_10k_x_5(b *testing.B) {
	ctx := context.Background()

	// 5 categories
	smallSchema := &storage.TableSchema{
		Name: "categories",
		Columns: []storage.Column{
			{Name: "cat_id", Type: storage.TypeInt},
			{Name: "cat_name", Type: storage.TypeVarChar},
		},
	}
	smallColMap := NewColumnIndexMap(smallSchema, "c")
	smallTuples := make([]Tuple, 5)
	for i := 0; i < 5; i++ {
		smallTuples[i] = Tuple{
			Values: []any{int64(i + 1), fmt.Sprintf("category_%d", i+1)},
			Index:  smallColMap,
		}
	}

	// 10,000 products
	largeSchema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "prod_id", Type: storage.TypeInt},
			{Name: "cat_id", Type: storage.TypeInt},
			{Name: "price", Type: storage.TypeInt},
		},
	}
	largeColMap := NewColumnIndexMap(largeSchema, "p")
	largeTuples := make([]Tuple, 10000)
	for i := 0; i < 10000; i++ {
		largeTuples[i] = Tuple{
			Values: []any{int64(i + 1), int64((i % 5) + 1), int64(100 + i)},
			Index:  largeColMap,
		}
	}

	b.Run("SmallTableBuild_Inverted", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			leftIter := &mockSliceIterator{tuples: smallTuples, schema: []string{"c.cat_id", "c.cat_name"}, colMap: smallColMap}
			rightIter := &mockSliceIterator{tuples: largeTuples, schema: []string{"p.prod_id", "p.cat_id", "p.price"}, colMap: largeColMap}
			hj := NewHashJoinIterator(leftIter, rightIter, "INNER", "c.cat_id", "p.cat_id")
			hj.SetBuildSide("LEFT")

			_ = hj.Open(ctx)
			count := 0
			for {
				_, err := hj.Next(ctx)
				if err != nil {
					break
				}
				count++
			}
			_ = hj.Close()
			if count != 10000 {
				b.Fatalf("expected 10000 rows, got %d", count)
			}
		}
	})

	b.Run("SmallTableBuild_Standard", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			leftIter := &mockSliceIterator{tuples: largeTuples, schema: []string{"p.prod_id", "p.cat_id", "p.price"}, colMap: largeColMap}
			rightIter := &mockSliceIterator{tuples: smallTuples, schema: []string{"c.cat_id", "c.cat_name"}, colMap: smallColMap}
			hj := NewHashJoinIterator(leftIter, rightIter, "INNER", "p.cat_id", "c.cat_id")
			hj.SetBuildSide("RIGHT")

			_ = hj.Open(ctx)
			count := 0
			for {
				_, err := hj.Next(ctx)
				if err != nil {
					break
				}
				count++
			}
			_ = hj.Close()
			if count != 10000 {
				b.Fatalf("expected 10000 rows, got %d", count)
			}
		}
	})
}

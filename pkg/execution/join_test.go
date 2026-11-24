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

func TestHashJoin_FlatArrayDirectProbe_Invariants(t *testing.T) {
	ctx := context.Background()

	t.Run("DuplicateKeys1toN", func(t *testing.T) {
		buildSchema := &storage.TableSchema{
			Name: "b",
			Columns: []storage.Column{
				{Name: "k", Type: storage.TypeInt},
				{Name: "val", Type: storage.TypeInt},
			},
		}
		buildColMap := NewColumnIndexMap(buildSchema, "b")
		buildTuples := make([]Tuple, 300)
		for i := 0; i < 300; i++ {
			buildTuples[i] = Tuple{
				Values: []any{int64(i % 10), int64(i)},
				Index:  buildColMap,
			}
		}

		probeSchema := &storage.TableSchema{
			Name: "p",
			Columns: []storage.Column{
				{Name: "k", Type: storage.TypeInt},
			},
		}
		probeColMap := NewColumnIndexMap(probeSchema, "p")
		probeTuples := make([]Tuple, 10)
		for i := 0; i < 10; i++ {
			probeTuples[i] = Tuple{
				Values: []any{int64(i)},
				Index:  probeColMap,
			}
		}

		leftIter := &mockSliceIterator{tuples: probeTuples, schema: []string{"p.k"}, colMap: probeColMap}
		rightIter := &mockSliceIterator{tuples: buildTuples, schema: []string{"b.k", "b.val"}, colMap: buildColMap}
		hj := NewHashJoinIterator(leftIter, rightIter, "INNER", "p.k", "b.k")
		if err := hj.Open(ctx); err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer hj.Close()

		if !hj.isFlatIntTable {
			t.Fatalf("expected isFlatIntTable to be true for 300 items")
		}

		count := 0
		for {
			_, err := hj.Next(ctx)
			if err != nil {
				break
			}
			count++
		}
		if count != 300 {
			t.Fatalf("expected 300 joined rows (10 probe * 30 matches), got %d", count)
		}
	})

	t.Run("NegativeAndBoundaryKeys", func(t *testing.T) {
		keys := []int64{-1000, -1, 0, 1, 99999999}
		buildSchema := &storage.TableSchema{
			Name: "b",
			Columns: []storage.Column{
				{Name: "k", Type: storage.TypeInt},
			},
		}
		buildColMap := NewColumnIndexMap(buildSchema, "b")
		buildTuples := make([]Tuple, len(keys))
		probeTuples := make([]Tuple, len(keys))
		for i, k := range keys {
			buildTuples[i] = Tuple{Values: []any{k}, Index: buildColMap}
			probeTuples[i] = Tuple{Values: []any{k}, Index: buildColMap}
		}

		leftIter := &mockSliceIterator{tuples: probeTuples, schema: []string{"b.k"}, colMap: buildColMap}
		rightIter := &mockSliceIterator{tuples: buildTuples, schema: []string{"b.k"}, colMap: buildColMap}
		hj := NewHashJoinIterator(leftIter, rightIter, "INNER", "b.k", "b.k")
		if err := hj.Open(ctx); err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer hj.Close()

		if !hj.isFlatIntTable {
			t.Fatalf("expected isFlatIntTable to be true")
		}

		count := 0
		for {
			_, err := hj.Next(ctx)
			if err != nil {
				break
			}
			count++
		}
		if count != len(keys) {
			t.Fatalf("expected %d rows, got %d", len(keys), count)
		}
	})

	t.Run("StringProbeIntBuildCoercion", func(t *testing.T) {
		buildSchema := &storage.TableSchema{
			Name: "b",
			Columns: []storage.Column{
				{Name: "k", Type: storage.TypeInt},
			},
		}
		buildColMap := NewColumnIndexMap(buildSchema, "b")
		buildTuples := make([]Tuple, 50)
		for i := 0; i < 50; i++ {
			buildTuples[i] = Tuple{Values: []any{int64(i + 1)}, Index: buildColMap}
		}

		probeSchema := &storage.TableSchema{
			Name: "p",
			Columns: []storage.Column{
				{Name: "str_k", Type: storage.TypeVarChar},
			},
		}
		probeColMap := NewColumnIndexMap(probeSchema, "p")
		probeTuples := make([]Tuple, 50)
		for i := 0; i < 50; i++ {
			probeTuples[i] = Tuple{Values: []any{fmt.Sprintf("%d", i+1)}, Index: probeColMap}
		}

		leftIter := &mockSliceIterator{tuples: probeTuples, schema: []string{"p.str_k"}, colMap: probeColMap}
		rightIter := &mockSliceIterator{tuples: buildTuples, schema: []string{"b.k"}, colMap: buildColMap}
		hj := NewHashJoinIterator(leftIter, rightIter, "INNER", "p.str_k", "b.k")
		if err := hj.Open(ctx); err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer hj.Close()

		if !hj.isFlatIntTable {
			t.Fatalf("expected isFlatIntTable to be true")
		}

		count := 0
		for {
			_, err := hj.Next(ctx)
			if err != nil {
				break
			}
			count++
		}
		if count != 50 {
			t.Fatalf("expected 50 joined rows with string-to-int coercion, got %d", count)
		}
	})

	t.Run("Threshold1000Boundary", func(t *testing.T) {
		// 1000 rows -> isFlatIntTable == true
		{
			sch := &storage.TableSchema{Name: "t", Columns: []storage.Column{{Name: "id", Type: storage.TypeInt}}}
			colMap := NewColumnIndexMap(sch, "t")
			tuples1000 := make([]Tuple, 1000)
			for i := 0; i < 1000; i++ {
				tuples1000[i] = Tuple{Values: []any{int64(i + 1)}, Index: colMap}
			}
			lIter := &mockSliceIterator{tuples: tuples1000[:10], schema: []string{"t.id"}, colMap: colMap}
			rIter := &mockSliceIterator{tuples: tuples1000, schema: []string{"t.id"}, colMap: colMap}
			hj := NewHashJoinIterator(lIter, rIter, "INNER", "t.id", "t.id")
			if err := hj.Open(ctx); err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			if !hj.isFlatIntTable {
				t.Fatalf("expected isFlatIntTable to be true for exactly 1000 rows")
			}
			_ = hj.Close()
		}

		// 1001 rows -> isFlatIntTable == false (graceful fallback to intHashTable)
		{
			sch := &storage.TableSchema{Name: "t", Columns: []storage.Column{{Name: "id", Type: storage.TypeInt}}}
			colMap := NewColumnIndexMap(sch, "t")
			tuples1001 := make([]Tuple, 1001)
			for i := 0; i < 1001; i++ {
				tuples1001[i] = Tuple{Values: []any{int64(i + 1)}, Index: colMap}
			}
			lIter := &mockSliceIterator{tuples: tuples1001[:10], schema: []string{"t.id"}, colMap: colMap}
			rIter := &mockSliceIterator{tuples: tuples1001, schema: []string{"t.id"}, colMap: colMap}
			hj := NewHashJoinIterator(lIter, rIter, "INNER", "t.id", "t.id")
			if err := hj.Open(ctx); err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			if hj.isFlatIntTable {
				t.Fatalf("expected isFlatIntTable to be false for 1001 rows (exceeds 1000)")
			}
			count := 0
			for {
				_, err := hj.Next(ctx)
				if err != nil {
					break
				}
				count++
			}
			if count != 10 {
				t.Fatalf("expected 10 joined rows from fallback map, got %d", count)
			}
			_ = hj.Close()
		}
	})
}

func BenchmarkHashJoin_10k_x_500(b *testing.B) {
	ctx := context.Background()

	// 500 build rows
	buildSchema := &storage.TableSchema{
		Name: "categories",
		Columns: []storage.Column{
			{Name: "cat_id", Type: storage.TypeInt},
			{Name: "cat_name", Type: storage.TypeVarChar},
		},
	}
	buildColMap := NewColumnIndexMap(buildSchema, "c")
	buildTuples := make([]Tuple, 500)
	for i := 0; i < 500; i++ {
		buildTuples[i] = Tuple{
			Values: []any{int64(i + 1), fmt.Sprintf("category_%d", i+1)},
			Index:  buildColMap,
		}
	}

	// 10,000 probe rows
	probeSchema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "prod_id", Type: storage.TypeInt},
			{Name: "cat_id", Type: storage.TypeInt},
			{Name: "price", Type: storage.TypeInt},
		},
	}
	probeColMap := NewColumnIndexMap(probeSchema, "p")
	probeTuples := make([]Tuple, 10000)
	for i := 0; i < 10000; i++ {
		probeTuples[i] = Tuple{
			Values: []any{int64(i + 1), int64((i % 500) + 1), int64(100 + i)},
			Index:  probeColMap,
		}
	}

	b.Run("FlatArray_500Build", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			leftIter := &mockSliceIterator{tuples: probeTuples, schema: []string{"p.prod_id", "p.cat_id", "p.price"}, colMap: probeColMap}
			rightIter := &mockSliceIterator{tuples: buildTuples, schema: []string{"c.cat_id", "c.cat_name"}, colMap: buildColMap}
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

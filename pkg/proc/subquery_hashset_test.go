package proc

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestSubqueryHashSet_NormalizationAndLookup(t *testing.T) {
	set := NewSubqueryHashSet(100)

	// 1. Integer normalization
	set.Add(int(10))
	set.Add(int32(20))
	set.Add(int64(30))
	set.Add(uint(40))

	// 2. Float normalization
	set.Add(float64(50.5))
	set.Add(float32(60.25))

	// 3. String & Byte slice normalization
	set.Add("hello")
	set.Add([]byte("world"))

	// 4. storage.Value
	set.Add(storage.IntValue(100))
	set.Add(storage.StringValue("storage_str"))

	// Verification
	tests := []struct {
		val   any
		isNot bool
		want  bool
	}{
		{int64(10), false, true},
		{int32(10), false, true},
		{int64(20), false, true},
		{int64(30), false, true},
		{int64(40), false, true},
		{float64(50.5), false, true},
		{"hello", false, true},
		{[]byte("world"), false, true},
		{int64(100), false, true},
		{"storage_str", false, true},

		// NOT IN tests
		{int64(10), true, false},
		{int64(999), true, true},
		{"non_existent", true, true},

		// Non-matching IN tests
		{int64(999), false, false},
		{"non_existent", false, false},
	}

	for _, tc := range tests {
		normKey, isNull := NormalizeSubqueryKey(tc.val)
		got := set.Evaluate(normKey, isNull, tc.isNot)
		if got != tc.want {
			t.Errorf("Evaluate(%v, isNot=%v): got %v, want %v", tc.val, tc.isNot, got, tc.want)
		}
	}
}

func TestSubqueryHashSet_ThreeValuedLogicWithNull(t *testing.T) {
	// Set containing NULL
	setWithNull := NewSubqueryHashSet(10)
	setWithNull.Add(int64(1))
	setWithNull.Add(int64(2))
	setWithNull.Add(nil) // Contains NULL

	if !setWithNull.HasNull {
		t.Fatalf("expected HasNull to be true")
	}

	// 1. Target found in set
	// 1 IN (1, 2, NULL) -> TRUE
	normKey, isNull := NormalizeSubqueryKey(int64(1))
	if !setWithNull.Evaluate(normKey, isNull, false) {
		t.Errorf("expected 1 IN (1, 2, NULL) to be true")
	}
	// 1 NOT IN (1, 2, NULL) -> FALSE
	if setWithNull.Evaluate(normKey, isNull, true) {
		t.Errorf("expected 1 NOT IN (1, 2, NULL) to be false")
	}

	// 2. Target NOT found in set with NULL:
	// 3 IN (1, 2, NULL) -> UNKNOWN (false in WHERE)
	normKey3, isNull3 := NormalizeSubqueryKey(int64(3))
	if setWithNull.Evaluate(normKey3, isNull3, false) {
		t.Errorf("expected 3 IN (1, 2, NULL) to evaluate to false (UNKNOWN)")
	}
	// 3 NOT IN (1, 2, NULL) -> UNKNOWN (false in WHERE)
	if setWithNull.Evaluate(normKey3, isNull3, true) {
		t.Errorf("expected 3 NOT IN (1, 2, NULL) to evaluate to false (UNKNOWN)")
	}

	// 3. NULL target:
	// NULL IN (1, 2, NULL) -> UNKNOWN (false in WHERE)
	nullKey, isNullTarget := NormalizeSubqueryKey(nil)
	if setWithNull.Evaluate(nullKey, isNullTarget, false) {
		t.Errorf("expected NULL IN (...) to evaluate to false")
	}
	// NULL NOT IN (1, 2, NULL) -> UNKNOWN (false in WHERE)
	if setWithNull.Evaluate(nullKey, isNullTarget, true) {
		t.Errorf("expected NULL NOT IN (...) to evaluate to false")
	}
}

func TestSubqueryHashSet_BoundedCapacityLimit(t *testing.T) {
	set := NewSubqueryHashSet(MaxSubqueryHashSetCapacity)

	for i := 0; i < MaxSubqueryHashSetCapacity; i++ {
		ok := set.Add(int64(i))
		if !ok {
			t.Fatalf("expected Add(%d) to succeed within capacity", i)
		}
	}

	// 10,001-th element must fail
	ok := set.Add(int64(MaxSubqueryHashSetCapacity + 1))
	if ok {
		t.Fatalf("expected Add to fail when exceeding MaxSubqueryHashSetCapacity (%d)", MaxSubqueryHashSetCapacity)
	}
}

func TestSubqueryHashSet_RegistryLifecycle(t *testing.T) {
	set := NewSubqueryHashSet(10)
	set.Add(int64(42))

	token := RegisterSubqueryHashSet(set)
	if token == "" {
		t.Fatalf("expected valid token, got empty")
	}

	retrieved, ok := LookupSubqueryHashSet(token)
	if !ok || retrieved != set {
		t.Fatalf("LookupSubqueryHashSet failed to retrieve registered set")
	}

	UnregisterSubqueryHashSet(token)
	_, okAfterDelete := LookupSubqueryHashSet(token)
	if okAfterDelete {
		t.Fatalf("expected token to be unregistered")
	}
}

func TestSubqueryHashSet_CrossTypeCoercion(t *testing.T) {
	set := NewSubqueryHashSet(10)
	set.Add("100") // string
	set.Add(int64(200)) // int64

	// 1. int target matching string in set (e.g. STRING_SPLIT result)
	normKeyInt, isNull := NormalizeSubqueryKey(int64(100))
	if !set.Evaluate(normKeyInt, isNull, false) {
		t.Errorf("expected int64(100) to match string '100'")
	}

	// 2. string target matching int in set
	normKeyStr, isNull := NormalizeSubqueryKey("200")
	if !set.Evaluate(normKeyStr, isNull, false) {
		t.Errorf("expected string '200' to match int64(200)")
	}
}

package engine

import "lionsql/pkg/proc"

// MaxSubqueryHashSetCapacity is the strict upper bound on rows materialized into an in-memory hashset ([RULE:BoundedBuffers]).
const MaxSubqueryHashSetCapacity = proc.MaxSubqueryHashSetCapacity

// SubqueryHashSet is an alias to proc.SubqueryHashSet ([RULE:ModularAbstraction]).
type SubqueryHashSet = proc.SubqueryHashSet

var (
	// NewSubqueryHashSet initializes a bounded subquery hash set with pre-allocated capacity.
	NewSubqueryHashSet = proc.NewSubqueryHashSet

	// NormalizeSubqueryKey converts any runtime value into its canonical hashable type.
	NormalizeSubqueryKey = proc.NormalizeSubqueryKey

	// RegisterSubqueryHashSet records a materialized subquery hashset and returns a unique query token.
	RegisterSubqueryHashSet = proc.RegisterSubqueryHashSet

	// LookupSubqueryHashSet retrieves a materialized subquery hashset by unique token.
	LookupSubqueryHashSet = proc.LookupSubqueryHashSet

	// UnregisterSubqueryHashSet cleans up a materialized subquery hashset.
	UnregisterSubqueryHashSet = proc.UnregisterSubqueryHashSet
)

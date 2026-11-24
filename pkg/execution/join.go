package execution

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

type hashMatch struct {
	tuple Tuple
	idx   int
}

type flatIntMatch struct {
	key   int64
	tuple Tuple
	idx   int
}

// flatHashSlot represents a single slot in an open-addressing, linear-probing flat hash table
// optimized for small build tables (<= 1,000 rows).
// All slots reside in a single contiguous slice fitting within CPU L1/L2 cache (56 bytes per slot).
type flatHashSlot struct {
	key   int64
	tuple Tuple
	idx   int
	used  bool
}

// splitmix64 computes a 64-bit pseudo-random hash for an integer key in 1-2 ns.
func splitmix64(x uint64) uint64 {
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// nextPowerOf2 returns the smallest power of 2 that is >= v.
func nextPowerOf2(v int) int {
	if v <= 1 {
		return 1
	}
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v |= v >> 32
	v++
	return v
}

// HashJoinIterator implements in-memory Hash Join between two relations.
// By default, the right child (inner) is buffered into a hash map during Open(),
// and the left child (outer) is probed tuple-by-tuple during Next().
// When buildSide is set to "LEFT", the roles are inverted: the left child is
// buffered into the hash map and the right child is probed.
type HashJoinIterator struct {
	left             Iterator // Outer / Probe (or Build if buildSide == "LEFT")
	right            Iterator // Inner / Build (or Probe if buildSide == "LEFT")
	joinType         string   // "INNER", "LEFT", "RIGHT", "FULL", "SEMI", "ANTI"
	buildSide        string   // "RIGHT" (default) or "LEFT"
	leftKey          string
	rightKey         string
	leftKeyIdx       int
	rightKeyIdx      int
	leftKeyParts     []string
	leftKeyIdxs      []int
	rightKeyParts    []string
	rightKeyIdxs     []int
	intHashTable     map[int64][]hashMatch
	hashTable        map[string][]hashMatch
	flatIntTable     []flatHashSlot
	flatIntMask      int
	isFlatIntTable   bool
	matchesBuf       []hashMatch
	allRightTuples   []Tuple
	matchedRight     []bool
	unmatchedIdx     int
	rightHasNullKey  bool
	currentProbe     Tuple
	currentMatches   []hashMatch
	matchIdx         int
	emittedAny       bool
	workMem          int64
	buildMemory      int64
	colIndexMap      *ColumnIndexMap
	inChunk          Chunk
	inIdx            int
	outChunk         Chunk
	outIdx           int
	arena            *storage.Arena
	ownedArena       *storage.Arena
	joinFilter       string
	parsedJoinFilter proc.Expr
	hasJoinFilter    bool
}

// NewHashJoinIterator creates a new Hash Join operator.
func NewHashJoinIterator(left, right Iterator, joinType, leftKey, rightKey string) *HashJoinIterator {
	jType := strings.ToUpper(strings.TrimSpace(joinType))
	if jType == "" {
		jType = "INNER"
	}
	return &HashJoinIterator{
		left:         left,
		right:        right,
		joinType:     jType,
		buildSide:    "RIGHT",
		leftKey:      strings.TrimSpace(leftKey),
		rightKey:     strings.TrimSpace(rightKey),
		leftKeyIdx:   -1,
		rightKeyIdx:  -1,
		hashTable:    make(map[string][]hashMatch),
		intHashTable: make(map[int64][]hashMatch),
		workMem:      DefaultWorkMem,
	}
}

// SetBuildSide configures which relation is built into the hash table ("RIGHT" (default) or "LEFT").
// For INNER and CROSS joins, building on the smaller relation significantly improves cache locality.
func (h *HashJoinIterator) SetBuildSide(side string) {
	s := strings.ToUpper(strings.TrimSpace(side))
	if s == "LEFT" {
		h.buildSide = "LEFT"
	} else {
		h.buildSide = "RIGHT"
	}
}

// SetArena configures an optional memory arena for zero-GC key string and merged tuple allocation.
func (h *HashJoinIterator) SetArena(a *storage.Arena) {
	h.arena = a
}

// SetWorkMem sets the maximum memory budget for the hash table.
func (h *HashJoinIterator) SetWorkMem(bytes int64) {
	if bytes > 0 {
		h.workMem = bytes
	}
}

// BuildMemory returns the bytes used by the hash table.
func (h *HashJoinIterator) BuildMemory() int64 {
	return h.buildMemory
}

// SetJoinFilter configures an extra boolean condition evaluated on merged candidate tuples.
func (h *HashJoinIterator) SetJoinFilter(filter string) {
	h.joinFilter = strings.TrimSpace(filter)
	if h.joinFilter != "" && h.joinFilter != "1=1" && h.joinFilter != "1 = 1" {
		h.hasJoinFilter = true
		p := proc.NewParser(proc.NewLexer(h.joinFilter))
		expr := p.ParseExpr()
		if expr != nil && len(p.Errors()) == 0 {
			h.parsedJoinFilter = proc.FoldConstants(expr)
		}
	}
}

func (h *HashJoinIterator) evalJoinFilter(merged Tuple) bool {
	if !h.hasJoinFilter {
		return true
	}
	if h.parsedJoinFilter != nil {
		return EvalTupleBoolExprWithContext(merged, h.parsedJoinFilter, nil, nil)
	}
	return EvalConditionTupleWithContext(merged, h.joinFilter, nil, nil)
}

func (h *HashJoinIterator) Open(ctx context.Context) error {
	if h.left == nil || h.right == nil {
		return fmt.Errorf("hash join: child iterators cannot be nil")
	}

	// Defensive check: verify key orientation between left and right iterators.
	if !strings.Contains(h.rightKey, ",") && !strings.Contains(h.leftKey, ",") {
		rMap := h.right.ColIndexMap()
		lMap := h.left.ColIndexMap()
		if rMap != nil {
			rightCol := h.rightKey
			rightHasQual := false
			if idx := strings.Index(rightCol, "."); idx != -1 {
				rightCol = rightCol[idx+1:]
				rightHasQual = true
			}
			leftCol := h.leftKey
			leftHasQual := false
			if idx := strings.Index(leftCol, "."); idx != -1 {
				leftCol = leftCol[idx+1:]
				leftHasQual = true
			}

			_, rHasRight := rMap.Index(h.rightKey)
			if !rHasRight && !rightHasQual {
				_, rHasRight = rMap.Index(rightCol)
			}
			_, rHasLeft := rMap.Index(h.leftKey)
			if !rHasLeft && !leftHasQual {
				_, rHasLeft = rMap.Index(leftCol)
			}

			var lHasLeft, lHasRight bool
			if lMap != nil {
				_, lHasLeft = lMap.Index(h.leftKey)
				if !lHasLeft && !leftHasQual {
					_, lHasLeft = lMap.Index(leftCol)
				}
				_, lHasRight = lMap.Index(h.rightKey)
				if !lHasRight && !rightHasQual {
					_, lHasRight = lMap.Index(rightCol)
				}
			}

			if (rHasLeft && !rHasRight) || (lMap != nil && rHasLeft && lHasRight && !lHasLeft) {
				h.leftKey, h.rightKey = h.rightKey, h.leftKey
			}
		}
	}

	h.leftKeyIdx = -1
	h.rightKeyIdx = -1
	h.leftKeyParts = nil
	h.leftKeyIdxs = nil
	h.rightKeyParts = nil
	h.rightKeyIdxs = nil

	if strings.Contains(h.rightKey, ",") {
		rawParts := strings.Split(h.rightKey, ",")
		h.rightKeyParts = make([]string, len(rawParts))
		h.rightKeyIdxs = make([]int, len(rawParts))
		rMap := h.right.ColIndexMap()
		for i, p := range rawParts {
			h.rightKeyParts[i] = strings.TrimSpace(p)
			h.rightKeyIdxs[i] = resolveColIndex(rMap, h.rightKeyParts[i])
		}
	} else {
		h.rightKeyIdx = resolveColIndex(h.right.ColIndexMap(), h.rightKey)
	}

	if strings.Contains(h.leftKey, ",") {
		rawParts := strings.Split(h.leftKey, ",")
		h.leftKeyParts = make([]string, len(rawParts))
		h.leftKeyIdxs = make([]int, len(rawParts))
		lMap := h.left.ColIndexMap()
		for i, p := range rawParts {
			h.leftKeyParts[i] = strings.TrimSpace(p)
			h.leftKeyIdxs[i] = resolveColIndex(lMap, h.leftKeyParts[i])
		}
	} else {
		h.leftKeyIdx = resolveColIndex(h.left.ColIndexMap(), h.leftKey)
	}

	if h.arena == nil {
		h.ownedArena = storage.GetArena()
		h.arena = h.ownedArena
	}

	buildIter := h.right
	probeIter := h.left
	buildKey := h.rightKey
	buildKeyIdx := h.rightKeyIdx
	buildKeyParts := h.rightKeyParts
	buildKeyIdxs := h.rightKeyIdxs

	if h.buildSide == "LEFT" {
		buildIter = h.left
		probeIter = h.right
		buildKey = h.leftKey
		buildKeyIdx = h.leftKeyIdx
		buildKeyParts = h.leftKeyParts
		buildKeyIdxs = h.leftKeyIdxs
	}

	// 1. Build phase: drain build relation into hash table
	if err := buildIter.Open(ctx); err != nil {
		return fmt.Errorf("hash join build open error: %w", err)
	}

	isFull := h.joinType == "FULL" || strings.HasPrefix(h.joinType, "FULL")
	isRight := h.joinType == "RIGHT" || strings.HasPrefix(h.joinType, "RIGHT")
	trackRight := (isFull || isRight) && h.buildSide != "LEFT"

	h.hashTable = make(map[string][]hashMatch)
	h.intHashTable = make(map[int64][]hashMatch)
	h.flatIntTable = nil
	h.flatIntMask = 0
	h.isFlatIntTable = false
	h.allRightTuples = nil
	h.matchedRight = nil
	h.unmatchedIdx = 0
	h.buildMemory = 0
	h.rightHasNullKey = false
	h.inChunk.Reset()
	h.inIdx = 0
	h.outChunk.Reset()
	h.outIdx = 0

	var flatItems []flatIntMatch

	inChunk := GetChunk()
	defer PutChunk(inChunk)
	for {
		select {
		case <-ctx.Done():
			_ = buildIter.Close()
			return ctx.Err()
		default:
		}

		err := PullChunk(ctx, buildIter, inChunk, DefaultChunkSize)
		if err != nil && err != io.EOF {
			_ = buildIter.Close()
			return fmt.Errorf("hash join build read error: %w", err)
		}
		if inChunk.Len() == 0 && err == io.EOF {
			break
		}

		for _, tuple := range inChunk.Tuples {
			tSize := EstimateTupleSize(tuple)
			if h.workMem > 0 && h.buildMemory+tSize > h.workMem {
				_ = buildIter.Close()
				return fmt.Errorf("hash join memory budget exceeded: %s > work_mem (%s)",
					FormatMemSize(h.buildMemory+tSize), FormatMemSize(h.workMem))
			}
			h.buildMemory += tSize

			rIdx := -1
			if trackRight {
				rIdx = len(h.allRightTuples)
				h.allRightTuples = append(h.allRightTuples, tuple)
				h.matchedRight = append(h.matchedRight, false)
			}

			if h.joinType == "CROSS" || (buildKey == "" && len(buildKeyParts) == 0) {
				h.hashTable[""] = append(h.hashTable[""], hashMatch{tuple: tuple, idx: rIdx})
				continue
			}

			if len(buildKeyParts) > 0 {
				kStr, found := extractTupleKeyFast(tuple, buildKeyParts, buildKeyIdxs, h.arena)
				if !found {
					h.rightHasNullKey = true
					continue
				}
				h.hashTable[kStr] = append(h.hashTable[kStr], hashMatch{tuple: tuple, idx: rIdx})
			} else if strings.Contains(buildKey, ",") {
				kStr, found := extractTupleKey(tuple, buildKey, h.arena)
				if !found {
					h.rightHasNullKey = true
					continue
				}
				h.hashTable[kStr] = append(h.hashTable[kStr], hashMatch{tuple: tuple, idx: rIdx})
			} else {
				keyVal, found := getTupleValFast(tuple, buildKeyIdx, buildKey)
				if !found || keyVal == nil {
					h.rightHasNullKey = true
					continue
				}
				if iKey, isInt := extractIntKey(keyVal); isInt {
					h.intHashTable[iKey] = append(h.intHashTable[iKey], hashMatch{tuple: tuple, idx: rIdx})
					flatItems = append(flatItems, flatIntMatch{key: iKey, tuple: tuple, idx: rIdx})
				} else {
					kStr := formatKeyVal(keyVal)
					if h.arena != nil {
						kStr = h.arena.AllocString(kStr)
					}
					h.hashTable[kStr] = append(h.hashTable[kStr], hashMatch{tuple: tuple, idx: rIdx})
				}
			}
		}

		if err == io.EOF {
			break
		}
	}
	_ = buildIter.Close()

	// [PERF-FLAT-ARRAY-HASH-JOIN-1] For build tables <= 1,000 rows with integer keys,
	// build an open-addressing, linear-probing flat hash table (Power-of-2 capacity, load factor <= 50%).
	// The entire table resides in contiguous memory in CPU L1/L2 cache, executing probes in ~1ns with zero pointer chasing.
	const maxFlatArrayBuildSize = 1000
	if len(flatItems) > 0 && len(flatItems) <= maxFlatArrayBuildSize && len(h.hashTable) == 0 {
		capSize := nextPowerOf2(len(flatItems) * 2)
		if capSize < 16 {
			capSize = 16
		}
		h.flatIntMask = capSize - 1
		h.flatIntTable = make([]flatHashSlot, capSize)
		for _, item := range flatItems {
			slot := int(splitmix64(uint64(item.key)) & uint64(h.flatIntMask))
			for h.flatIntTable[slot].used {
				slot = (slot + 1) & h.flatIntMask
			}
			h.flatIntTable[slot] = flatHashSlot{
				key:   item.key,
				tuple: item.tuple,
				idx:   item.idx,
				used:  true,
			}
		}
		h.isFlatIntTable = true
		h.intHashTable = nil // Free map memory; flatIntTable handles 100% of probes
	}

	// 2. Probe phase: open probe child
	if err := probeIter.Open(ctx); err != nil {
		return fmt.Errorf("hash join probe open error: %w", err)
	}

	if h.joinType == "SEMI" || h.joinType == "ANTI" {
		h.colIndexMap = h.left.ColIndexMap()
	} else {
		h.colIndexMap = MergeColumnIndexMaps(h.left.ColIndexMap(), h.right.ColIndexMap())
	}
	h.currentProbe = Tuple{}
	h.currentMatches = nil
	h.matchIdx = 0
	h.emittedAny = false
	return nil
}

// probeFlatInt performs direct L1/L2 cache-resident linear probing on the flat hash table.
// Returns true if one or more matching build tuples are found.
func (h *HashJoinIterator) probeFlatInt(key int64) bool {
	h.matchesBuf = h.matchesBuf[:0]
	slot := int(splitmix64(uint64(key)) & uint64(h.flatIntMask))
	for h.flatIntTable[slot].used {
		if h.flatIntTable[slot].key == key {
			h.matchesBuf = append(h.matchesBuf, hashMatch{
				tuple: h.flatIntTable[slot].tuple,
				idx:   h.flatIntTable[slot].idx,
			})
		}
		slot = (slot + 1) & h.flatIntMask
	}
	if len(h.matchesBuf) > 0 {
		h.currentMatches = h.matchesBuf
		return true
	}
	return false
}

func (h *HashJoinIterator) pullProbe(ctx context.Context) (Tuple, error) {
	if h.inIdx < len(h.inChunk.Tuples) {
		t := h.inChunk.Tuples[h.inIdx]
		h.inIdx++
		return t, nil
	}
	h.inChunk.Reset()
	h.inIdx = 0
	iter := h.left
	if h.buildSide == "LEFT" {
		iter = h.right
	}
	err := PullChunk(ctx, iter, &h.inChunk, DefaultChunkSize)
	if err != nil && err != io.EOF {
		return Tuple{}, err
	}
	if len(h.inChunk.Tuples) == 0 && err == io.EOF {
		return Tuple{}, io.EOF
	}
	t := h.inChunk.Tuples[0]
	h.inIdx = 1
	return t, nil
}

func (h *HashJoinIterator) mergeProbeAndMatch(probe, match Tuple) Tuple {
	if h.buildSide == "LEFT" {
		return mergeTuplesWithArena(match, probe, h.colIndexMap, h.arena)
	}
	return mergeTuplesWithArena(probe, match, h.colIndexMap, h.arena)
}

func (h *HashJoinIterator) nextTuple(ctx context.Context) (Tuple, error) {
	isFull := h.joinType == "FULL" || strings.HasPrefix(h.joinType, "FULL")
	isRight := h.joinType == "RIGHT" || strings.HasPrefix(h.joinType, "RIGHT")
	trackRight := (isFull || isRight) && h.buildSide != "LEFT"

	probeKey := h.leftKey
	probeKeyIdx := h.leftKeyIdx
	probeKeyParts := h.leftKeyParts
	probeKeyIdxs := h.leftKeyIdxs
	if h.buildSide == "LEFT" {
		probeKey = h.rightKey
		probeKeyIdx = h.rightKeyIdx
		probeKeyParts = h.rightKeyParts
		probeKeyIdxs = h.rightKeyIdxs
	}

	for {
		select {
		case <-ctx.Done():
			return Tuple{}, ctx.Err()
		default:
		}

		// If current probe tuple has pending matches, emit next match (only for standard joins)
		if h.joinType != "SEMI" && h.joinType != "ANTI" {
			for !h.currentProbe.IsNil() && h.matchIdx < len(h.currentMatches) {
				m := h.currentMatches[h.matchIdx]
				h.matchIdx++
				merged := h.mergeProbeAndMatch(h.currentProbe, m.tuple)
				if h.evalJoinFilter(merged) {
					h.emittedAny = true
					if trackRight && m.idx >= 0 && m.idx < len(h.matchedRight) {
						h.matchedRight[m.idx] = true
					}
					return merged, nil
				}
			}
		}

		// Pull next probe tuple from outer relation
		probeTuple, err := h.pullProbe(ctx)
		if err != nil {
			if err == io.EOF && trackRight {
				for h.unmatchedIdx < len(h.allRightTuples) {
					idx := h.unmatchedIdx
					h.unmatchedIdx++
					if !h.matchedRight[idx] {
						rTuple := h.allRightTuples[idx]
						leftColsCount := 0
						if h.left.ColIndexMap() != nil {
							leftColsCount = len(h.left.ColIndexMap().Columns())
						} else {
							leftColsCount = len(h.left.Schema())
						}
						return mergeNullsWithTupleAndArena(leftColsCount, rTuple, h.colIndexMap, h.arena), nil
					}
				}
			}
			return Tuple{}, err // propagates io.EOF or context error
		}

		h.currentProbe = probeTuple
		h.currentMatches = nil
		h.matchIdx = 0
		h.emittedAny = false

		hasMatch := false
		var found bool
		if h.joinType == "CROSS" || (h.leftKey == "" && h.rightKey == "") {
			found = true
			if matches, exists := h.hashTable[""]; exists && len(matches) > 0 {
				hasMatch = true
				h.currentMatches = matches
			}
		} else if len(probeKeyParts) > 0 {
			var kStr string
			kStr, found = extractTupleKeyFast(probeTuple, probeKeyParts, probeKeyIdxs, nil)
			if found {
				if matches, exists := h.hashTable[kStr]; exists && len(matches) > 0 {
					hasMatch = true
					h.currentMatches = matches
				}
			}
		} else if strings.Contains(probeKey, ",") {
			var kStr string
			kStr, found = extractTupleKey(probeTuple, probeKey, nil)
			if found {
				if matches, exists := h.hashTable[kStr]; exists && len(matches) > 0 {
					hasMatch = true
					h.currentMatches = matches
				}
			}
		} else {
			keyVal, ok := getTupleValFast(probeTuple, probeKeyIdx, probeKey)
			if ok && keyVal != nil {
				found = true
				if iKey, isInt := extractIntKey(keyVal); isInt {
					if h.isFlatIntTable {
						hasMatch = h.probeFlatInt(iKey)
					} else if matches, exists := h.intHashTable[iKey]; exists && len(matches) > 0 {
						hasMatch = true
						h.currentMatches = matches
					} else if len(h.hashTable) > 0 {
						kStr := strconv.FormatInt(iKey, 10)
						if matches, exists := h.hashTable[kStr]; exists && len(matches) > 0 {
							hasMatch = true
							h.currentMatches = matches
						}
					}
				} else {
					kStr := formatKeyVal(keyVal)
					if matches, exists := h.hashTable[kStr]; exists && len(matches) > 0 {
						hasMatch = true
						h.currentMatches = matches
					} else if h.isFlatIntTable {
						if pInt, err := strconv.ParseInt(kStr, 10, 64); err == nil {
							hasMatch = h.probeFlatInt(pInt)
						}
					} else if len(h.intHashTable) > 0 {
						if pInt, err := strconv.ParseInt(kStr, 10, 64); err == nil {
							if matches, exists := h.intHashTable[pInt]; exists && len(matches) > 0 {
								hasMatch = true
								h.currentMatches = matches
							}
						}
					}
				}
			}
		}

		if h.joinType == "SEMI" {
			if hasMatch {
				for _, m := range h.currentMatches {
					merged := h.mergeProbeAndMatch(probeTuple, m.tuple)
					if h.evalJoinFilter(merged) {
						h.currentProbe = Tuple{}
						return probeTuple, nil
					}
				}
			}
			continue
		}

		if h.joinType == "ANTI" {
			if h.rightHasNullKey || !found {
				h.currentProbe = Tuple{}
				continue
			}
			if !hasMatch {
				h.currentProbe = Tuple{}
				return probeTuple, nil
			}
			matchedAny := false
			for _, m := range h.currentMatches {
				merged := h.mergeProbeAndMatch(probeTuple, m.tuple)
				if h.evalJoinFilter(merged) {
					matchedAny = true
					break
				}
			}
			if !matchedAny {
				h.currentProbe = Tuple{}
				return probeTuple, nil
			}
			continue
		}

		for h.matchIdx < len(h.currentMatches) {
			m := h.currentMatches[h.matchIdx]
			h.matchIdx++
			merged := h.mergeProbeAndMatch(h.currentProbe, m.tuple)
			if h.evalJoinFilter(merged) {
				h.emittedAny = true
				if trackRight && m.idx >= 0 && m.idx < len(h.matchedRight) {
					h.matchedRight[m.idx] = true
				}
				return merged, nil
			}
		}

		// If LEFT JOIN or FULL JOIN and no matches, emit probe tuple with NULLs
		isLeftOrFull := h.joinType == "LEFT" || strings.HasPrefix(h.joinType, "LEFT") || isFull
		if isLeftOrFull && !h.emittedAny {
			h.currentProbe = Tuple{}
			rightColsCount := 0
			if h.right.ColIndexMap() != nil {
				rightColsCount = len(h.right.ColIndexMap().Columns())
			} else {
				rightColsCount = len(h.right.Schema())
			}
			return mergeTuplesWithNullsAndArena(probeTuple, rightColsCount, h.colIndexMap, h.arena), nil
		}
	}
}

func (h *HashJoinIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	dest.Reset()
	for dest.Len() < DefaultChunkSize {
		t, err := h.nextTuple(ctx)
		if err != nil {
			if dest.Len() > 0 && err == io.EOF {
				return nil
			}
			return err
		}
		dest.Tuples = append(dest.Tuples, t)
	}
	return nil
}

func (h *HashJoinIterator) Next(ctx context.Context) (Tuple, error) {
	if h.outIdx < len(h.outChunk.Tuples) {
		t := h.outChunk.Tuples[h.outIdx]
		h.outIdx++
		return t, nil
	}
	h.outChunk.Reset()
	h.outIdx = 0
	if err := h.NextChunk(ctx, &h.outChunk); err != nil {
		return Tuple{}, err
	}
	if len(h.outChunk.Tuples) == 0 {
		return Tuple{}, io.EOF
	}
	t := h.outChunk.Tuples[0]
	h.outIdx = 1
	return t, nil
}

func (h *HashJoinIterator) Close() error {
	h.hashTable = nil
	h.intHashTable = nil
	h.flatIntTable = nil
	h.flatIntMask = 0
	h.isFlatIntTable = false
	h.matchesBuf = nil
	h.allRightTuples = nil
	h.matchedRight = nil
	h.currentProbe = Tuple{}
	h.currentMatches = nil
	h.inChunk.Reset()
	h.inIdx = 0
	h.outChunk.Reset()
	h.outIdx = 0
	if h.ownedArena != nil {
		storage.PutArena(h.ownedArena)
		h.ownedArena = nil
		h.arena = nil
	}
	var errLeft, errRight error
	if h.left != nil {
		errLeft = h.left.Close()
	}
	if h.right != nil {
		errRight = h.right.Close()
	}
	if errLeft != nil {
		return errLeft
	}
	return errRight
}

func (h *HashJoinIterator) Schema() []string {
	if h.joinType == "SEMI" || h.joinType == "ANTI" {
		if h.left != nil {
			return h.left.Schema()
		}
		return nil
	}
	if h.colIndexMap != nil {
		return h.colIndexMap.Columns()
	}
	var s []string
	if h.left != nil {
		s = append(s, h.left.Schema()...)
	}
	if h.right != nil {
		s = append(s, h.right.Schema()...)
	}
	return s
}

func (h *HashJoinIterator) ColIndexMap() *ColumnIndexMap {
	if h.joinType == "SEMI" || h.joinType == "ANTI" {
		if h.left != nil {
			return h.left.ColIndexMap()
		}
		return nil
	}
	if h.colIndexMap == nil && h.left != nil && h.right != nil {
		h.colIndexMap = MergeColumnIndexMaps(h.left.ColIndexMap(), h.right.ColIndexMap())
	}
	return h.colIndexMap
}

// IndexNestedLoopJoinIterator executes a nested loop join by using an index on the inner table.
type IndexNestedLoopJoinIterator struct {
	outer            Iterator
	tx               storage.Transaction
	innerTable       string
	innerAlias       string
	innerIndex       *storage.IndexSchema
	innerSchema      *storage.TableSchema
	leftKey          string
	joinType         string
	filterCond       string
	currentOuter     Tuple
	innerCursor      storage.Cursor
	hasMatch         bool
	colIndexMap      *ColumnIndexMap
	innerColIndexMap *ColumnIndexMap
	inChunk          Chunk
	inIdx            int
	outChunk         Chunk
	outIdx           int
	innerPlan        *planner.PlanNode
}

// NewIndexNestedLoopJoinIterator creates a new Index Nested Loop Join operator.
func NewIndexNestedLoopJoinIterator(outer Iterator, tx storage.Transaction, innerTable, innerAlias string, innerIndex *storage.IndexSchema, innerSchema *storage.TableSchema, leftKey, joinType string) *IndexNestedLoopJoinIterator {
	jType := strings.ToUpper(strings.TrimSpace(joinType))
	if jType == "" {
		jType = "INNER"
	}
	if innerAlias == "" {
		innerAlias = innerTable
	}
	return &IndexNestedLoopJoinIterator{
		outer:       outer,
		tx:          tx,
		innerTable:  innerTable,
		innerAlias:  innerAlias,
		innerIndex:  innerIndex,
		innerSchema: innerSchema,
		leftKey:     leftKey,
		joinType:    jType,
	}
}

// SetFilter sets an optional filter condition evaluated on inner tuples.
func (n *IndexNestedLoopJoinIterator) SetFilter(cond string) {
	n.filterCond = strings.TrimSpace(cond)
}

// SetInnerPlan sets an optional inner PlanNode for profiling inner loop execution.
func (n *IndexNestedLoopJoinIterator) SetInnerPlan(plan *planner.PlanNode) {
	n.innerPlan = plan
}

func (n *IndexNestedLoopJoinIterator) Open(ctx context.Context) error {
	if n.outer == nil {
		return fmt.Errorf("index nested loop join: outer iterator is nil")
	}
	if err := n.outer.Open(ctx); err != nil {
		return err
	}
	n.innerColIndexMap = NewColumnIndexMap(n.innerSchema, n.innerAlias)
	var outerMap *ColumnIndexMap
	if n.outer != nil {
		outerMap = n.outer.ColIndexMap()
	}
	if n.joinType == "SEMI" || n.joinType == "ANTI" {
		n.colIndexMap = outerMap
	} else {
		n.colIndexMap = MergeColumnIndexMaps(outerMap, n.innerColIndexMap)
	}
	n.currentOuter = Tuple{}
	n.innerCursor = nil
	n.hasMatch = false
	n.inChunk.Reset()
	n.inIdx = 0
	n.outChunk.Reset()
	n.outIdx = 0
	return nil
}

func (n *IndexNestedLoopJoinIterator) pullOuter(ctx context.Context) (Tuple, error) {
	if n.inIdx < len(n.inChunk.Tuples) {
		t := n.inChunk.Tuples[n.inIdx]
		n.inIdx++
		return t, nil
	}
	n.inChunk.Reset()
	n.inIdx = 0
	err := PullChunk(ctx, n.outer, &n.inChunk, DefaultChunkSize)
	if err != nil && err != io.EOF {
		return Tuple{}, err
	}
	if len(n.inChunk.Tuples) == 0 && err == io.EOF {
		return Tuple{}, io.EOF
	}
	t := n.inChunk.Tuples[0]
	n.inIdx = 1
	return t, nil
}

func (n *IndexNestedLoopJoinIterator) nextTuple(ctx context.Context) (Tuple, error) {
	for {
		select {
		case <-ctx.Done():
			return Tuple{}, ctx.Err()
		default:
		}

		if n.innerCursor != nil {
			for n.innerCursor.Next() {
				rec, err := n.innerCursor.Record()
				if err != nil {
					break
				}
				innerTuple := FormatRecord(rec, n.innerSchema, n.innerAlias, n.innerColIndexMap)
				if n.filterCond != "" && !EvalConditionTuple(innerTuple, n.filterCond) {
					continue
				}

				n.hasMatch = true
				if n.innerPlan != nil {
					n.innerPlan.ActualRows++
				}
				if n.joinType == "SEMI" {
					_ = n.innerCursor.Close()
					n.innerCursor = nil
					out := n.currentOuter
					n.currentOuter = Tuple{}
					return out, nil
				}
				if n.joinType == "ANTI" {
					_ = n.innerCursor.Close()
					n.innerCursor = nil
					n.currentOuter = Tuple{}
					break
				}
				return mergeTuples(n.currentOuter, innerTuple, n.colIndexMap), nil
			}

			if n.innerCursor != nil {
				_ = n.innerCursor.Close()
				n.innerCursor = nil
			}

			if n.joinType == "ANTI" {
				if !n.hasMatch && !n.currentOuter.IsNil() {
					out := n.currentOuter
					n.currentOuter = Tuple{}
					return out, nil
				}
				n.currentOuter = Tuple{}
				continue
			}

			// If LEFT JOIN and had no match for this outer tuple
			if (n.joinType == "LEFT" || strings.HasPrefix(n.joinType, "LEFT")) && !n.hasMatch && !n.currentOuter.IsNil() {
				out := mergeTuplesWithNulls(n.currentOuter, len(n.innerSchemaColumns()), n.colIndexMap)
				n.currentOuter = Tuple{}
				return out, nil
			}
			n.currentOuter = Tuple{}
		}

		// Pull next outer tuple
		outerTuple, err := n.pullOuter(ctx)
		if err != nil {
			return Tuple{}, err // io.EOF or context cancellation
		}

		n.currentOuter = outerTuple
		n.hasMatch = false

		keyVal, found := lookupVal(outerTuple, n.leftKey)
		stVal := toStorageValFromAny(keyVal)
		if !found || keyVal == nil || stVal.IsNull || stVal.IsNullValue() {
			if n.joinType == "ANTI" {
				n.currentOuter = Tuple{}
				return outerTuple, nil
			}
			if n.joinType == "LEFT" || strings.HasPrefix(n.joinType, "LEFT") {
				n.currentOuter = Tuple{}
				return mergeTuplesWithNulls(outerTuple, len(n.innerSchemaColumns()), n.colIndexMap), nil
			}
			continue
		}
		scanStart := time.Now()
		cur, err := n.tx.IndexScan(n.innerTable, n.innerIndex.Name, &stVal, &stVal)
		if err != nil {
			return Tuple{}, err
		}
		if n.innerPlan != nil {
			n.innerPlan.ActualLoops++
			n.innerPlan.ActualDurationUS += time.Since(scanStart).Microseconds()
		}
		n.innerCursor = cur
	}
}

func (n *IndexNestedLoopJoinIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	dest.Reset()
	for dest.Len() < DefaultChunkSize {
		t, err := n.nextTuple(ctx)
		if err != nil {
			if dest.Len() > 0 && err == io.EOF {
				return nil
			}
			return err
		}
		dest.Tuples = append(dest.Tuples, t)
	}
	return nil
}

func (n *IndexNestedLoopJoinIterator) Next(ctx context.Context) (Tuple, error) {
	if n.outIdx < len(n.outChunk.Tuples) {
		t := n.outChunk.Tuples[n.outIdx]
		n.outIdx++
		return t, nil
	}
	n.outChunk.Reset()
	n.outIdx = 0
	if err := n.NextChunk(ctx, &n.outChunk); err != nil {
		return Tuple{}, err
	}
	if len(n.outChunk.Tuples) == 0 {
		return Tuple{}, io.EOF
	}
	t := n.outChunk.Tuples[0]
	n.outIdx = 1
	return t, nil
}

func (n *IndexNestedLoopJoinIterator) Close() error {
	n.inChunk.Reset()
	n.inIdx = 0
	n.outChunk.Reset()
	n.outIdx = 0
	if n.innerCursor != nil {
		_ = n.innerCursor.Close()
		n.innerCursor = nil
	}
	if n.outer != nil {
		return n.outer.Close()
	}
	return nil
}

func (n *IndexNestedLoopJoinIterator) Schema() []string {
	if n.joinType == "SEMI" || n.joinType == "ANTI" {
		if n.outer != nil {
			return n.outer.Schema()
		}
		return nil
	}
	if n.colIndexMap != nil {
		return n.colIndexMap.Columns()
	}
	var s []string
	if n.outer != nil {
		s = append(s, n.outer.Schema()...)
	}
	s = append(s, n.innerSchemaColumns()...)
	return s
}

func (n *IndexNestedLoopJoinIterator) ColIndexMap() *ColumnIndexMap {
	if n.joinType == "SEMI" || n.joinType == "ANTI" {
		if n.outer != nil {
			return n.outer.ColIndexMap()
		}
		return nil
	}
	if n.colIndexMap == nil {
		n.innerColIndexMap = NewColumnIndexMap(n.innerSchema, n.innerAlias)
		var outerMap *ColumnIndexMap
		if n.outer != nil {
			outerMap = n.outer.ColIndexMap()
		}
		n.colIndexMap = MergeColumnIndexMaps(outerMap, n.innerColIndexMap)
	}
	return n.colIndexMap
}

func (n *IndexNestedLoopJoinIterator) innerSchemaColumns() []string {
	if n.innerSchema == nil {
		return nil
	}
	cols := make([]string, len(n.innerSchema.Columns))
	for i, c := range n.innerSchema.Columns {
		cols[i] = c.Name
	}
	return cols
}

func mergeTuples(left, right Tuple, combinedIndex *ColumnIndexMap) Tuple {
	return mergeTuplesWithArena(left, right, combinedIndex, nil)
}

func mergeTuplesWithArena(left, right Tuple, combinedIndex *ColumnIndexMap, arena *storage.Arena) Tuple {
	left.Materialize()
	right.Materialize()
	leftLen := len(left.Values)
	if left.Index != nil && len(left.Index.cols) > leftLen {
		leftLen = len(left.Index.cols)
	}
	rightLen := len(right.Values)
	if right.Index != nil && len(right.Index.cols) > rightLen {
		rightLen = len(right.Index.cols)
	}
	var vals []any
	if arena != nil {
		vals = arena.AllocAnySlice(leftLen + rightLen)
	} else {
		vals = make([]any, leftLen+rightLen)
	}
	copy(vals, left.Values)
	copy(vals[leftLen:], right.Values)
	return Tuple{
		Values: vals,
		Index:  combinedIndex,
	}
}

func mergeTuplesWithNulls(left Tuple, rightColsCount int, combinedIndex *ColumnIndexMap) Tuple {
	return mergeTuplesWithNullsAndArena(left, rightColsCount, combinedIndex, nil)
}

func mergeTuplesWithNullsAndArena(left Tuple, rightColsCount int, combinedIndex *ColumnIndexMap, arena *storage.Arena) Tuple {
	left.Materialize()
	leftLen := len(left.Values)
	if left.Index != nil && len(left.Index.cols) > leftLen {
		leftLen = len(left.Index.cols)
	}
	var vals []any
	if arena != nil {
		vals = arena.AllocAnySlice(leftLen + rightColsCount)
	} else {
		vals = make([]any, leftLen+rightColsCount)
	}
	copy(vals, left.Values)
	return Tuple{
		Values: vals,
		Index:  combinedIndex,
	}
}

func mergeNullsWithTuple(leftColsCount int, right Tuple, combinedIndex *ColumnIndexMap) Tuple {
	return mergeNullsWithTupleAndArena(leftColsCount, right, combinedIndex, nil)
}

func mergeNullsWithTupleAndArena(leftColsCount int, right Tuple, combinedIndex *ColumnIndexMap, arena *storage.Arena) Tuple {
	right.Materialize()
	rightLen := len(right.Values)
	if right.Index != nil && len(right.Index.cols) > rightLen {
		rightLen = len(right.Index.cols)
	}
	var vals []any
	if arena != nil {
		vals = arena.AllocAnySlice(leftColsCount + rightLen)
	} else {
		vals = make([]any, leftColsCount+rightLen)
	}
	copy(vals[leftColsCount:], right.Values)
	return Tuple{
		Values: vals,
		Index:  combinedIndex,
	}
}

func toStorageValFromAny(v any) storage.Value {
	if v == nil {
		return storage.NullValue(storage.TypeUnknown)
	}
	switch val := v.(type) {
	case storage.Value:
		return val
	case int64:
		return storage.IntValue(val)
	case int:
		return storage.IntValue(int64(val))
	case int32:
		return storage.IntValue(int64(val))
	case int16:
		return storage.IntValue(int64(val))
	case int8:
		return storage.IntValue(int64(val))
	case uint64:
		return storage.IntValue(int64(val))
	case uint32:
		return storage.IntValue(int64(val))
	case uint16:
		return storage.IntValue(int64(val))
	case uint8:
		return storage.IntValue(int64(val))
	case uint:
		return storage.IntValue(int64(val))
	case float64:
		return storage.FloatValue(val)
	case float32:
		return storage.FloatValue(float64(val))
	case storage.Decimal:
		return storage.DecimalValueFromDecimal(val)
	case string:
		return storage.StringValue(val)
	case bool:
		return storage.BoolValue(val)
	default:
		return storage.StringValue(fmt.Sprintf("%v", val))
	}
}

func formatKeyVal(val any) string {
	switch v := val.(type) {
	case string:
		return strings.ToLower(strings.TrimRight(v, " "))
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case int32:
		return strconv.Itoa(int(v))
	case storage.Value:
		if v.IsNull {
			return "NULL"
		}
		if v.Type == storage.TypeInt {
			return strconv.FormatInt(v.IVal, 10)
		}
		if v.Type == storage.TypeVarChar {
			return strings.ToLower(strings.TrimRight(v.SVal, " "))
		}
		return strings.ToLower(strings.TrimRight(fmt.Sprintf("%v", v), " "))
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return strings.ToLower(strings.TrimRight(fmt.Sprintf("%v", val), " "))
	}
}

var joinKeyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 256)
		return &b
	},
}

func appendKeyVal(buf []byte, val any) []byte {
	switch v := val.(type) {
	case string:
		s := strings.TrimRight(v, " ")
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c >= 'A' && c <= 'Z' {
				c += 32
			}
			buf = append(buf, c)
		}
		return buf
	case int64:
		return strconv.AppendInt(buf, v, 10)
	case int:
		return strconv.AppendInt(buf, int64(v), 10)
	case int32:
		return strconv.AppendInt(buf, int64(v), 10)
	case storage.Value:
		if v.IsNull {
			return append(buf, "NULL"...)
		}
		if v.Type == storage.TypeInt {
			return strconv.AppendInt(buf, v.IVal, 10)
		}
		if v.Type == storage.TypeVarChar {
			s := strings.TrimRight(v.SVal, " ")
			for i := 0; i < len(s); i++ {
				c := s[i]
				if c >= 'A' && c <= 'Z' {
					c += 32
				}
				buf = append(buf, c)
			}
			return buf
		}
		return append(buf, strings.ToLower(strings.TrimRight(fmt.Sprintf("%v", v), " "))...)
	case float64:
		return strconv.AppendFloat(buf, v, 'f', -1, 64)
	default:
		return append(buf, strings.ToLower(strings.TrimRight(fmt.Sprintf("%v", val), " "))...)
	}
}

func extractTupleKeyFast(t Tuple, parts []string, idxs []int, arena *storage.Arena) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}
	if len(parts) == 1 {
		colIdx := -1
		if len(idxs) > 0 {
			colIdx = idxs[0]
		}
		keyVal, found := getTupleValFast(t, colIdx, parts[0])
		if !found || keyVal == nil {
			return "", false
		}
		kStr := formatKeyVal(keyVal)
		if arena != nil {
			return arena.AllocString(kStr), true
		}
		return kStr, true
	}

	bufPtr := joinKeyBufPool.Get().(*[]byte)
	buf := (*bufPtr)[:0]

	for i, part := range parts {
		colIdx := -1
		if idxs != nil && i < len(idxs) {
			colIdx = idxs[i]
		}
		val, found := getTupleValFast(t, colIdx, part)
		if !found || val == nil {
			*bufPtr = buf
			joinKeyBufPool.Put(bufPtr)
			return "", false
		}
		if i > 0 {
			buf = append(buf, 0x00)
		}
		buf = appendKeyVal(buf, val)
	}

	res := string(buf)
	if cap(buf) <= 4096 {
		*bufPtr = buf
		joinKeyBufPool.Put(bufPtr)
	}
	if arena != nil {
		return arena.AllocString(res), true
	}
	return res, true
}

func extractTupleKey(t Tuple, key string, arena *storage.Arena) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	if !strings.Contains(key, ",") {
		keyVal, found := lookupVal(t, key)
		if !found || keyVal == nil {
			return "", false
		}
		kStr := formatKeyVal(keyVal)
		if arena != nil {
			return arena.AllocString(kStr), true
		}
		return kStr, true
	}

	parts := strings.Split(key, ",")
	var buf strings.Builder
	for i, part := range parts {
		part = strings.TrimSpace(part)
		val, found := lookupVal(t, part)
		if !found || val == nil {
			return "", false
		}
		if i > 0 {
			buf.WriteByte('\x00')
		}
		buf.WriteString(formatKeyVal(val))
	}
	res := buf.String()
	if arena != nil {
		return arena.AllocString(res), true
	}
	return res, true
}

func resolveColIndex(cmap *ColumnIndexMap, key string) int {
	if cmap == nil {
		return -1
	}
	key = strings.Trim(strings.TrimSpace(key), "\"`[]")
	if idx, ok := cmap.Index(key); ok && idx >= 0 {
		return idx
	}
	if dot := strings.Index(key, "."); dot != -1 {
		bare := key[dot+1:]
		if idx, ok := cmap.Index(bare); ok && idx >= 0 {
			return idx
		}
	}
	return -1
}

func getTupleValFast(t Tuple, colIdx int, keyName string) (any, bool) {
	if !strings.Contains(keyName, ",") && colIdx >= 0 && colIdx < len(t.Values) {
		val := t.Values[colIdx]
		if val != nil {
			return val, true
		}
		if t.Lazy != nil {
			sv, err := t.Lazy.Get(colIdx)
			if err == nil {
				formatted := formatStorageVal(sv)
				t.Values[colIdx] = formatted
				return formatted, true
			}
		}
		return nil, false
	}
	return lookupVal(t, keyName)
}

func extractIntKey(val any) (int64, bool) {
	if val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	case int16:
		return int64(v), true
	case int8:
		return int64(v), true
	case uint64:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint:
		return int64(v), true
	case float64:
		if v == float64(int64(v)) {
			return int64(v), true
		}
		return 0, false
	case storage.Value:
		if v.IsNull {
			return 0, false
		}
		if v.Type == storage.TypeInt {
			return v.IVal, true
		}
		if v.Type == storage.TypeFloat && v.FVal == float64(int64(v.FVal)) {
			return int64(v.FVal), true
		}
		return 0, false
	default:
		return 0, false
	}
}

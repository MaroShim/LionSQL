# Architecture Plan: SQLancer Native Engine Port (TLP & PQS Oracles)

## 1. Goal
Integrate SQLancer's two most powerful logic-bug detection oracles—**TLP (Ternary Logic Partitioning)** and **PQS (Pivoted Query Synthesis)**—directly into LionSQL's native Go fuzzing framework (`pkg/speckit` & `cmd/spec_fuzz`) without external Java/JVM dependencies.

---

## 2. Theoretical Background & Oracles

### 1) TLP (Ternary Logic Partitioning - Rigger & Su, OOPSLA 2020)
In three-valued SQL logic ($TRUE, FALSE, NULL$), any query $Q = \text{SELECT } \text{cols} \text{ FROM } T \text{ WHERE } \phi$ can be partitioned into three disjoint queries:
1. $Q_{true} = \text{SELECT } \text{cols} \text{ FROM } T \text{ WHERE } \phi$
2. $Q_{false} = \text{SELECT } \text{cols} \text{ FROM } T \text{ WHERE } \text{NOT } (\phi)$
3. $Q_{null} = \text{SELECT } \text{cols} \text{ FROM } T \text{ WHERE } (\phi) \text{ IS NULL}$

**Invariant**:
$$\text{COUNT}(Q) = \text{COUNT}(Q_{true}) + \text{COUNT}(Q_{false}) + \text{COUNT}(Q_{null}) = |T|$$
Furthermore, for aggregation variants (TLP-Aggregate):
$$\text{SUM}_{all}(Q) = \text{SUM}(Q_{true}) + \text{SUM}(Q_{false}) + \text{SUM}(Q_{null})$$
If $\text{Rows}(Q) \ne \text{Rows}(Q_{true} \cup Q_{false} \cup Q_{null})$, there is a 3VL, index scan, or Volcano filtering bug in the query engine.

### 2) PQS (Pivoted Query Synthesis - Rigger & Su, OOPSLA 2020)
1. Pick a randomly selected "pivot row" $r$ from table $T$.
2. Synthesize a complex randomized predicate $\phi$ by evaluating expressions against the actual values of $r$.
3. Check whether $r \models \phi$ ($TRUE$). If not, negate or adjust terms so $r$ is guaranteed to satisfy $\phi$.
4. **Invariant**: When executing $\text{SELECT } * \text{ FROM } T \text{ WHERE } \phi$, the result set MUST contain the pivot row $r$. If $r \notin \text{Results}$, the database incorrectly pruned the row due to optimizer or index bug.

---

## 3. Implementation Blueprint

### Phase 1: Grammar Extensions (`pkg/speckit/grammar/`)
- `grammar/query.go`:
  - `TLPPair` struct and `GenerateTLPPair(rng, table, maxDepth)`:
    - Generates $Q_{orig}$, $Q_{true}$, $Q_{false}$, $Q_{null}$ and union check queries.
  - `PQSPair` struct and `GeneratePQSPair(rng, table, pivotRow, maxDepth)`:
    - Generates truth-evaluated expressions guaranteed to match the pivot row.

### Phase 2: Oracle Implementations (`pkg/speckit/oracle/`)
- `oracle/oracle.go`: Add `DiscrepancyTLP` and `DiscrepancyPQS` discrepancy types.
- `oracle/tlp.go`: `CheckTLP(coord, sessID, pair, tableDDL, insertDMLs) (*Discrepancy, error)`.
- `oracle/pqs.go`: `CheckPQS(coord, sessID, pair, tableDDL, insertDMLs) (*Discrepancy, error)`.
- `oracle/oracle_test.go`: Unit tests verifying that TLP and PQS detect simulated 3VL/optimizer discrepancies.

### Phase 3: Runner Integration (`pkg/speckit/runner/` & `cmd/spec_fuzz/`)
- Update `runner.Config.OracleType` to support: `"norec"`, `"tlp"`, `"pqs"`, `"dualengine"`, `"all"`.
- Support multiplexed worker loops executing all oracles.
- Update `minimizer` to generate appropriate TDD Red test suites for TLP and PQS discrepancies.

### Phase 4: Automated Verification (Build ➔ Test ➔ Commit)
- Run `go test -v -race ./pkg/speckit/...`.
- Run `go build -buildvcs=false -o bin/spec_fuzz ./cmd/spec_fuzz`.
- Execute a smoke discovery run with `./bin/spec_fuzz -oracle all -duration 5s`.

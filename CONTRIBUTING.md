# Contributing to SQLion

Thank you for your interest in contributing to SQLion! This guide explains our engineering standards, architecture invariants, development workflow, and verification pipeline.

---

## 1. Development Prerequisites

- **Go**: Version 1.22 or higher.
- **WiredTiger (Optional for Cgo production backend)**:
  - macOS: `brew install wiredtiger`
  - Ubuntu/Debian: `sudo apt-get install libwiredtiger-dev`
- Note: SQLion can also be built and developed in pure Go without WiredTiger or Cgo:
  ```bash
  go build -buildvcs=false -o bin/server ./cmd/server
  ```

---

## 2. The 3-Stage Verification Pipeline

All pull requests, feature additions, refactorings, and bug fixes must strictly pass the **3-Stage Verification Pipeline** before being merged or committed:

```
[Code Modifications]
        │
        ▼
[Stage 1: Build] ──> Compiles both server and benchmark binaries
        │             go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
        │             go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench
        ▼
[Stage 2: Test] ───> 100% PASS across all packages with 0 data races
        │             go test -tags wiredtiger -count=1 -v -race ./...
        ▼
[Stage 3: Commit] ─> Conventional Commits format, strictly NO decorative emojis
```

### Stage 1: Compilation Integrity (Build)
```bash
go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench
```
Verifies strict type-checking, Cgo bindings, and compile invariants without VCS stamping overhead.

### Stage 2: Race-Free Test Verification (Test)
```bash
go test -tags wiredtiger -count=1 -v -race ./...
```
Must achieve **100% PASS across all packages with 0 data races**. Flaky tests or thread sanitizer race detections are unacceptable.

### Stage 3: Conventional Commit (Commit)
- Use standard Conventional Commits format:
  - `feat(scope): add new capability`
  - `fix(scope): resolve bug`
  - `refactor(scope): optimize internal logic`
  - `test(scope): add test coverage`
  - `docs(scope): update documentation`
- **Strictly avoid decorative emojis** in commit messages, comments, logs, and code (Rule 19).

---

## 3. Core Golden Rules for Contributors

Contributors must observe the following engineering golden rules:

1. **Non-Destructive File Safety (Atomic Saves)**:
   Any disk write (snapshots, backups, config files) must be staged to a temporary swap file (`*.tmp`) followed by atomic rename (`os.Rename`) to prevent corruption on unexpected halts.
2. **Deterministic Testing (Flake-Free Hermeticity)**:
   Never rely on arbitrary `time.Sleep` in tests. Synchronize deterministically using channels, `sync.WaitGroup`, or explicit condition polling.
3. **Hermetic Socket Testing**:
   When testing wire protocol interactions, use in-memory `net.Pipe()` duplex streams rather than binding physical TCP ports. This guarantees zero port collisions in parallel CI runners.
4. **Defensive Path Security**:
   Always normalize user-supplied file paths via `filepath.Clean` and guard against directory traversal (`..`).
5. **File Descriptor & Handle Hygiene**:
   Bind every opened file, cursor, pipe, and socket to deterministic cleanup via `defer Close()`.
6. **Explicit Error Visibility (No Silent Failures)**:
   Never silently swallow errors (`_ = err`). Always propagate errors or inform the caller through diagnostics or status bars.
7. **Defensive Indexing & Slicing**:
   Guard slice accesses with boundary checks (`0 <= idx < len(slice)`).
8. **Radical KISS & Minimal Diff**:
   Prefer simple, deletion-first solutions over complex translation layers or speculative abstractions.

---

## 4. Dual-Parser Architecture Invariant

SQLion enforces a **Dual-Parser Architecture**:
1. **Primary Fast-Path (Native Pratt Parser)**: `pkg/proc/lexer.go`, `pkg/proc/parser.go`.
2. **Secondary Fallback (Official ANTLR4 T-SQL Parser)**: `pkg/proc/parser/antlr/`.

### Guidelines for Syntax Changes:
- Any new T-SQL syntax extension should first be verified against the ANTLR4 grammar parser.
- When extending the native fast-path parser, verify dual parity in `pkg/proc/runtime/regression_test.go`.
- Maintain complete decoupling: `pkg/proc` must never directly import `pkg/proc/parser/antlr`.

---

## 5. Pull Request Checklist

Before submitting your pull request, verify:

- [ ] Code compiles cleanly via `go build -tags wiredtiger ...`.
- [ ] Full test suite passes 100% with race detector enabled (`go test -tags wiredtiger -race ./...`).
- [ ] All temporary files and resources are cleanly unlinked and closed in tests.
- [ ] New functionality is covered by deterministic unit or integration tests.
- [ ] Documentation (`doc/ARCHITECTURE.md`, `doc/ADR.md`, `README.md`) is updated if architectural changes were introduced.
- [ ] Commit messages follow Conventional Commits and contain zero decorative emojis.

# Antigravity Engineering Guidelines: Core Golden Rules

## Level 1: Data Safety, Crash Prevention & Security (Priority 1)
1. **Non-Destructive File Safety (Atomic Saves)**: File writes must be atomic via temporary swap files and atomic renames to prevent corruption on unexpected halts.
2. **Panic Recovery & Terminal Rescue Hooks**: Intercept runtime panics with global rescue hooks that restore terminal raw mode before printing diagnostics.
3. **Shell Injection Immunity (Execve Safety)**: Never invoke processes via raw shell strings (`sh -c`); pass parsed argument vectors directly to `exec.Command`.
4. **Clean Teardown & Process Slaying**: Terminate child processes cleanly on exit, escalating from graceful interrupt to `SIGKILL` within 1.5 seconds.
5. **Defensive Path Security & Permission Safety**: Normalize paths via `filepath.Clean` to prevent directory traversal and enforce non-executable `0644` permissions.
6. **Non-Regular File & Symlink Loop Safety**: Guard against cyclic symlink traversal and disallow opening non-regular files (pipes, sockets, devices) as text.
7. **Explicit Error Visibility (No Silent Failures)**: Never silently swallow errors (`_ = err`); always inform the user via status bar messages, logs, or diagnostics.
8. **Ephemeral Auto-Recovery & Session Safety**: Periodically sync dirty buffer snapshots to an isolated swap directory for crash recovery.
9. **External File Change Detection (Conflict Prevention)**: Track file modification timestamps (`mtime`) to warn users before overwriting externally changed files.
10. **Strict Lock Hierarchy (Deadlock Prevention)**: Acquire multiple mutexes strictly in canonical order and never invoke unknown callbacks while holding locks.
11. **Defensive Indexing & Overflow Guard**: Guard every slice access with defensive boundary checks (`0 <= idx < len`) and protect geometry/arithmetic against integer wrapping.
12. **Resource Teardown Idempotency (Double-Close Safety)**: Protect cleanup handlers with `sync.Once` to ensure idempotency and prevent double-close panics.
13. **Binary & Control Character Sanitization**: Detect binary files (NUL bytes) upon opening and render control characters safely as visible escape symbols (`^M`).

## Level 2: Core Architecture, Quality & Verification (Priority 2)
14. **Platform Primitives First (No Redundant Emulation)**: Rely on OS and standard library native primitives rather than reinventing them.
15. **Radical KISS, DRY & Deletion-First**: Keep solutions radically simple. Prefer solving problems by deleting unnecessary layers, ad-hoc flags, and fragile workarounds before writing new code. Enforce YAGNI and standard library primacy.
16. **Automated Verification & Completion Commit Policy (3-Stage Pipeline: Build ➔ Test ➔ Commit)**: All feature additions, refactoring, and code modifications must strictly pass the following 3 stages in order before committing (pure documentation/markdown (`*.md`) changes without source code modifications may bypass unnecessary builds and tests and be committed immediately):
    1. **Build**: `go build -buildvcs=false -o bin/<target> ./cmd/<target>` (compilation and type integrity verification)
    2. **Test**: `go test -v -race ./...` (100% PASS across all packages and 0 data races)
    3. **Commit**: Strictly adhere to Conventional Commits format with zero decorative emojis
17. **Deterministic Testing (Flake-Free Hermeticity)**: Never rely on arbitrary `time.Sleep` in tests; synchronize deterministically with channels or explicit polling.
18. **Documentation & Style Integrity (No Emojis)**: Strictly avoid decorative emojis in all documents, comments, logs, and commit messages; maintain an engineering tone.
19. **Modular Interface Abstraction (Open-Closed Principle)**: Decouple backend engines behind strict interface contracts so new extensions require zero core changes.
20. **File Descriptor & Handle Hygiene**: Bind every open file, pipe, and socket to deterministic cleanup (`defer Close()`) to prevent handle leakage.
21. **Bounded Call-Stack & Iterative Traversal**: Enforce strict recursion depth limits (<= 128) or use heap-backed iterative loops to prevent stack exhaustion.
22. **Headless Command-Line Interface Parity**: Support running core build, format, and check operations non-interactively via CLI flags with standard exit codes.

## Level 3: Bounded Resources, Concurrency & Performance (Priority 3)
23. **Bounded Buffers & Defensive Resource Limits**: Enforce explicit capacity limits on in-memory collections (undo stacks capped at 100, caches, scrollbacks, search results).
24. **Zero-Allocation Hot Paths (GC Pressure Mitigation)**: Preallocate and reuse scratch buffers (`sync.Pool`) across critical render and execution loops.
25. **Responsive Latency & Performance Budget**: Keep main hot-path dispatch latency under 16ms; never perform synchronous I/O on direct typing or event handling paths.
26. **Concurrency Safety & Race-Free Event Loops**: Confine state mutations strictly to safe loops and ensure parallel workers pass `go test -race`.
27. **Cooperative Cancellation & Context Timeouts**: Support immediate abort via `context.Context` for long-running operations with explicit timeouts.
28. **Zero-Orphan Concurrency & Goroutine Lifecycle**: Tether every spawned goroutine to a parent context and synchronize termination with `sync.WaitGroup`.
29. **Non-Blocking Channel Sinks & Backpressure**: Guard channel sends with `select/default` to prevent slow event consumers from hanging producer workers.
30. **Atomic Primitives for Control Flags**: Use `sync/atomic` primitives (`atomic.Bool`, `atomic.Int64`) for high-frequency status and cancellation polling.
31. **Direct Slice Indexing & Bounded Lookups**: Maintain direct slice indexing or pre-resolved slots to guarantee constant-time resolution on hot execution paths.
32. **Lexical Robustness & Syntax Fail-Safety**: Ensure tokenizers and parsers never panic on malformed input or unclosed tokens; degrade cleanly with structured diagnostics.

## Level 4: Cross-Platform Portability & Production Release (Priority 4)
33. **Backwards Compatibility & Safe Default Configurations**: Fall back to safe default settings when config files are missing, corrupted, or contain deprecated fields.
34. **Line Ending & Charset Invariance (CRLF / BOM Neutrality)**: Transparently handle Windows (`CRLF`) and Unix (`LF`) endings and strip UTF-8 BOM upon loading.
35. **Hermetic Cross-Platform Parity**: Maintain identical layouts, key semantics, and test behavior across macOS, Linux, and Windows.
36. **Pure Static Binaries (Self-Contained Executables)**: Default to self-contained compilation (`CGO_ENABLED=0` or static library linking) to eliminate dynamic runtime dependencies.
37. **Deterministic Configuration Hierarchy**: Resolve settings strictly via: CLI Arguments > Environment Variables > Config File > Built-in Defaults.
38. **Atomic Config Writes & Zero-Corruption**: Validate and stage configuration files to temporary storage before committing via atomic rename.
39. **XDG Base Directory Compliance**: Store configurations in `$XDG_CONFIG_HOME` (`~/.config`) and cache in `$XDG_CACHE_HOME` (`~/.cache`).
40. **Cumulative Changelog Discipline (Project Root CHANGELOG.md Maintenance)**: Maintain `CHANGELOG.md` at the project root, continuously logging all notable changes including feature additions, bug fixes, and refactoring across releases and versions (strictly adhering to the Keep a Changelog standard).
41. **Externalized State & Continuous Backlog Discipline (Project Root TODO.md / TODO.ko.md Auto-Sync)**: Never retain agreed tasks, architecture decisions (ADRs), bug remedies, or next steps solely in ephemeral conversation memory. Proactively externalize and auto-sync them to the project root TODO dashboard (`TODO.md` or `TODO.ko.md`) under explicit taxonomy IDs and status lifecycles (Pending ➔ In Progress ➔ Completed `[x]`), ensuring unbroken continuity and zero context bloat across sessions and model invocations.

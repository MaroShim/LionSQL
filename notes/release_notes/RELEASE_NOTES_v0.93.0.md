# LionSQL v0.93.0 Release Notes (`notes/RELEASE_NOTES_v0.93.0.md`)

> - **Initial Date**: 2026-11-16
> - **Release Version**: `v0.93.0`
> - **Milestone Theme**: LionSQL v0.93.0 Maintenance and Compatibility Release

---

## 1. Release Highlights

LionSQL `v0.93.0` incorporates cumulative stability hardening, performance optimizations, and ecosystem compatibility fixes.

---

## 2. Detailed Changes

See the detailed changelog delta below for commit-level records across procedural execution, storage durability, and wire protocols.

---

## 3. Verification & Compliance

1. **3-Stage Pipeline Verification (`[RULE:PipelineVerification]`)**:
   - Official release binaries built for macOS (`darwin-arm64`), Linux (`linux-amd64`), and Windows (`windows-amd64`).
   - `go test -tags wiredtiger -count=1 -race ./pkg/... ./test/slt_proc/...` 100% PASS (0 data races).

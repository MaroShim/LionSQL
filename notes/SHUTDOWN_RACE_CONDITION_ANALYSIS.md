# LionSQL Graceful Shutdown Race Condition & WiredTiger SIGSEGV Analysis

## 1. Incident Overview

- **Incident Date**: 2026-10-19
- **Crash Signal**: `SIGSEGV: segmentation violation (addr=0xad0, sigcode=2)`
- **Fault Address**: `addr=0xad0` (dereferencing field offset `0xad0` on destroyed connection)
- **Affected Subsystems**:
  - `pkg/wire` (PostgreSQL Wire Protocol v3 Listener)
  - `pkg/storage/wiredtiger` (WiredTiger Storage Engine CGO Binding)
  - `cmd/lionsql` (Graceful Shutdown Coordinator)

---

## 2. Crash Stack Trace & Register Analysis

### 2.1. Concurrently Crashing Goroutine (Goroutine 97)
```text
goroutine 97 gp=0x5e4f8f25e000 m=11 mp=0x5e4f8e8ed008 [syscall]:
runtime.cgocall(0x1059f8890, 0x5e4f8e8c3828)
	/opt/homebrew/Cellar/go/1.27.1/libexec/src/runtime/cgocall.go:167 +0x44 fp=0x5e4f8e8c37f0 sp=0x5e4f8e8c37b0 pc=0x104dddf44
sqlion/pkg/storage/wiredtiger._Cfunc_go_wt_session_close(0xc0c007168)
	_cgo_gotypes.go:608 +0x30 fp=0x5e4f8e8c3820 sp=0x5e4f8e8c37f0 pc=0x10576c550
sqlion/pkg/storage/wiredtiger.(*WTSession).Close.func2(...)
	/Users/maro/Projects/LionSQL/pkg/storage/wiredtiger/wt_engine.go:835
sqlion/pkg/storage/wiredtiger.(*WTSession).Close(0x5e4f8eccc1b0)
	/Users/maro/Projects/LionSQL/pkg/storage/wiredtiger/wt_engine.go:835 +0x264 fp=0x5e4f8e8c3910 sp=0x5e4f8e8c3820 pc=0x105771624
sqlion/pkg/engine.(*SessionPool).ReleaseSession(0x5e4f8e7ba510, {0x106cc7df8, 0x5e4f8eccc1b0})
	/Users/maro/Projects/LionSQL/pkg/engine/session_pool.go:180 +0x148 fp=0x5e4f8e8c3990 sp=0x5e4f8e8c3910 pc=0x105881038
sqlion/pkg/engine.(*Coordinator).CloseSession(0x5e4f8e7bc360, {0x5e4f8e6fdd90, 0x4})
	/Users/maro/Projects/LionSQL/pkg/engine/coordinator_impl.go:384 +0x4e4 fp=0x5e4f8e8c3ae0 sp=0x5e4f8e8c3990 pc=0x10586e3f4
sqlion/pkg/api.(*Server).CloseSession(0x58e8c3b38?, {0x5e4f8e6fdd90?, 0x5e4f8f25e000?})
	/Users/maro/Projects/LionSQL/pkg/api/server.go:357 +0x24 fp=0x5e4f8e8c3b10 sp=0x5e4f8e8c3ae0 pc=0x10596bd64
sqlion/pkg/wire.(*Server).handleConnection.deferwrap2()
	/Users/maro/Projects/LionSQL/pkg/wire/server.go:420 +0x28 fp=0x5e4f8e8c3b40 sp=0x5e4f8e8c3b10 pc=0x10590c7c8
...
sqlion/pkg/wire.(*Server).handleConnection(0x5e4f8e7b45a0, {0x106cd6768, 0x5e4f8e6ea3b8})
```

### 2.2. Shutdown Coordinator Goroutine (Goroutine 217)
```text
goroutine 217 gp=0x5e4f8ea43c20 m=7 mp=0x5e4f8e95f808 [syscall]:
runtime.cgocall(0x1059f81d0, 0x5e4f8e78beb8)
	/opt/homebrew/Cellar/go/1.27.1/libexec/src/runtime/cgocall.go:167 +0x44 fp=0x5e4f8e78be80 sp=0x5e4f8e78be40 pc=0x104dddf44
sqlion/pkg/storage/wiredtiger._Cfunc_go_wt_close(0x7dcc1c000)
	_cgo_gotypes.go:408 +0x30 fp=0x5e4f8e78beb0 sp=0x5e4f8e78be80 pc=0x10576be10
sqlion/pkg/storage/wiredtiger.(*Engine).Close.func1(...)
	/Users/maro/Projects/LionSQL/pkg/storage/wiredtiger/wt_engine.go:427
sqlion/pkg/storage/wiredtiger.(*Engine).Close(0x5e4f8ea3e0b0)
	/Users/maro/Projects/LionSQL/pkg/storage/wiredtiger/wt_engine.go:427 +0x15c fp=0x5e4f8e78bf50 sp=0x5e4f8e78beb0 pc=0x10576db7c
main.runServer.func4()
	/Users/maro/Projects/LionSQL/cmd/lionsql/server.go:829 +0x140 fp=0x5e4f8e78bfd0 sp=0x5e4f8e78bf50 pc=0x10599cec0
created by main.runServer in goroutine 1
	/Users/maro/Projects/LionSQL/cmd/lionsql/server.go:811 +0x3f34
```

### 2.3. Register Analysis
- `r21 = 0x7dcc1c000`: Address of `WT_CONNECTION` passed to `go_wt_close(0x7dcc1c000)` in Goroutine 217.
- `r0 = 0xc0c007168`: Address of `WT_SESSION` passed to `go_wt_session_close(0xc0c007168)` in Goroutine 97.
- `addr = 0xad0`: Offset `0xad0` inside the connection control structure (session list / connection lock).
- When `go_wt_close` ran, WiredTiger destroyed the `WT_CONNECTION` struct and freed its heap arenas. When `go_wt_session_close` dereferenced `session->conn` at offset `0xad0`, it accessed unmapped memory, resulting in `SIGSEGV`.

---

## 3. Root Cause Analysis

The failure stemmed from two concurrent design defects:

1. **PGWire Teardown Drain Lack (`pkg/wire/server.go`)**:
   - In `pkg/wire/server.go`, `Server.Close()` only closed the underlying network sockets (`c.Close()`) and immediately returned without synchronizing or waiting for the `handleConnection(conn)` goroutines to finish.
   - Contrast this with TDS (`pkg/wire/tds/server.go`), which tracked active connections via `sync.WaitGroup` and blocked in `Close()` until connections completed their deferred session cleanup.
   - Because `wireServer.Close()` returned immediately, `cmd/lionsql/server.go` proceeded to call `server.Close()` and `eng.Close()` while PGWire connections were still unwinding their defer stacks (`s.coordinator.CloseSession(sessID)`).

2. **WiredTiger Engine Active Session Drain Lack & Guard Absence (`pkg/storage/wiredtiger/wt_engine.go`)**:
   - `Engine` maintains `sessions map[*WTSession]struct{}` and `sessMu`, tracking open sessions.
   - However, `Engine.Close()` called `C.go_wt_close(e.conn)` directly without first iterating through and closing remaining `e.sessions`.
   - Furthermore, `WTSession.Close()` had no guard checking if the engine's connection had already been closed, unconditionally invoking `C.go_wt_session_close(s.sess)`.

---

## 4. Multi-Layered Remediation Plan

To permanently eliminate shutdown races and guarantees zero panics / zero SIGSEGV across all teardown paths:

### Layer 1: PGWire Server Goroutine Lifecycle Synchronization (`pkg/wire/server.go`)
- Add `wg sync.WaitGroup` to `wire.Server`.
- Call `s.wg.Add(1)` in `acceptLoop` before spawning `handleConnection`, and `defer s.wg.Done()`.
- In `wire.Server.Close()`, close listener and client sockets, then wait with a 1-second bounded timeout (`s.wg.Wait()`) before returning.

### Layer 2: WiredTiger Engine Session Drain (`pkg/storage/wiredtiger/wt_engine.go`)
- In `Engine.Close()`, before closing `e.conn`, acquire `e.sessMu`, snapshot all remaining active sessions, release the lock, and invoke `s.Close()` on each session.
- This ensures all C `WT_SESSION` pointers are closed cleanly while `e.conn` is still valid and operational.

### Layer 3: Connection State Atomic Guard (`pkg/storage/wiredtiger/wt_engine.go`)
- Use `connReleased atomic.Bool` in `Engine`.
- Mark `connReleased.Store(true)` right before invoking `C.go_wt_close(e.conn)`.
- In `WTSession.Close()`, check `if s.engine.connReleased.Load() { s.sess = nil; return nil }`. If the engine connection has already begun closing, bypass `C.go_wt_session_close` and clear pointers safely.

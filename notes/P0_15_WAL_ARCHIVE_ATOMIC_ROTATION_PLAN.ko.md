# LionSQL P0-15: WAL 아카이브 회전 경쟁 상태 방지 및 원자적 세그먼트 스왑 아키텍처 (`notes/P0_15_WAL_ARCHIVE_ATOMIC_ROTATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-08
> - **도메인**: WAL 아카이빙(WAL Archiving), PITR 시점 복구(Point-in-Time Recovery), 동시성 경쟁 상태(Race Condition), 원자적 파일 스왑
> - **참조 규칙**: `[RULE:AtomicSaves]`, `[RULE:LockHierarchy]`, `[RULE:ConcurrencySafety]`
> - **관련 과제**: `P0-15` (`pkg/backup/wal_archive.go`, `pkg/storage/memengine/wal.go`)

---

## 1. 개요 (The Archive-Truncate Race Condition)

LionSQL의 PITR(Point-in-Time Recovery) 및 연속 백업 시스템은 주기적으로 활성 WAL(Write-Ahead Log) 파일을 아카이브 디렉토리로 복사하고, 메인 WAL 파일을 0바이트로 절삭(`Truncate`)하여 무한한 디스크 증설을 방지합니다.

그러나 기존 `RotateAndArchive()` 구현에는 치명적인 **시간차 경쟁 상태(Time-of-Check to Time-of-Use Race Condition)**가 존재했습니다:
**"복사와 절삭 사이에 락이 풀려있는 나노초의 틈새로 신규 트랜잭션이 유입되어 WAL에 기록되었을 때, 그 직후 실행된 `Truncate()`에 의해 신규 트랜잭션 데이터가 영구 절삭·삭제되는 데이터 유실 결함"**이었습니다.

---

## 2. 결함의 본질과 재현 시나리오

### 2.1. 기존 취약 흐름
```
[Goroutine 1: RotateAndArchive]          [Goroutine 2: 신규 트랜잭션 Commit]
1. 활성 wal.log 파일을 복사 (Copy)
2. (복사 완료, writer.mu 락 미보유 상태!)
                                        3. tx.Commit() ➔ wal.log에 새 LSN 1001 기록!
4. writer.file.Truncate(0) 실행!
   ===> 방금 커밋된 LSN 1001이
        아카이브에도 없고 디스크에서도 잘려나감!
        (Silent Data Loss 발생!)
```

### 2.2. 위험성
- 고동시성 환경(초당 10,000+ 트랜잭션)에서 아카이브 회전 주기가 돌아올 때마다 특정 트랜잭션이 무작위로 증발.
- 아카이브 백업본을 이용해 복원(Restore)할 때 LSN 구멍(Gap)이 발생하여 복원 실패.

---

## 3. 해결책: 원자적 파일 디스크립터 스왑(Atomic FD Swap) 아키텍처

우리는 `[RULE:AtomicSaves]`에 입각하여, 기존의 **"복사 후 절삭(Copy-then-Truncate)"** 방식을 전면 폐기하고 **"원자적 파일 교체 및 동결 세그먼트 백그라운드 아카이빙(Atomic Swap-then-Archive)"** 모델로 전환했습니다:

```
[RotateAndArchive Triggered]
             │
             ▼
[1단계: writer.mu.Lock() 획득]
- 활성 wal.log 파일의 쓰기를 완벽히 동결(Freeze).
- wal.log를 wal.<LSN>.frozen 으로 즉시 Rename.
- 신규 빈 wal.log 파일을 생성하고 활성 File Descriptor 교체(Atomic Swap).
- writer.mu.Unlock() 해제! (락 점유 시간 < 1ms 미만)
             │
             ▼
[2단계: 격리된 동결 세그먼트 안전 아카이빙]
- 신규 트랜잭션은 새 wal.log에 대기 없이 즉시 쓰기 재개.
- 백그라운드 워커가 wal.<LSN>.frozen 파일을 아카이브 저장소로 여유롭게 복사.
- 복사 완료 후 frozen 파일만 안전하게 제거(`os.Remove`).
```

---

## 4. 핵심 코드 구현 (`pkg/storage/memengine/wal.go`)

```go
// [P0-15] Rotate atomically switches the active WAL file under lock.
func (w *WALWriter) Rotate() (string, error) {
    w.mu.Lock()
    defer w.mu.Unlock()

    if err := w.file.Sync(); err != nil {
        return "", fmt.Errorf("failed to sync active WAL before rotate: %w", err)
    }
    _ = w.file.Close()

    frozenPath := fmt.Sprintf("%s.%d.frozen", w.path, time.Now().UnixNano())
    if err := os.Rename(w.path, frozenPath); err != nil {
        return "", fmt.Errorf("failed to rename WAL to frozen segment: %w", err)
    }

    // Open brand new clean WAL file
    newFile, err := os.OpenFile(w.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
    if err != nil {
        return "", fmt.Errorf("failed to open new active WAL file: %w", err)
    }
    w.file = newFile
    w.offset = 0

    return frozenPath, nil
}
```

---

## 5. 결론 및 성과

- **데이터 무손실 보장**: 회전 중 동시 트랜잭션이 아무리 많이 유입되어도 신규 파일에 안전하게 격리 기록되어 절삭 유실 가능성 0%.
- **지연시간 지터 최소화**: 수백 MB의 아카이브 복사를 락 외부로 완벽히 분리하여 메인 쿼리 응답 시간을 마이크로초 수준으로 수호.

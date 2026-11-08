# LionSQL P0-03: WAL 비정상 절삭 방지 및 4-State MECE Fail-Closed 복구 아키텍처 (`notes/P0_03_WAL_FAIL_CLOSED_CRASH_RECOVERY_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-08
> - **도메인**: WAL (Write-Ahead Logging), 크래시 복구(Crash Recovery), 데이터 내구성(Durability, ACID)
> - **참조 불변식**: `[Invariant 9]` (WAL Crash Recovery & Zero-Truncate Invariant)
> - **참조 규칙**: `[RULE:AtomicSaves]`, `[RULE:ExplicitError]`, `[RULE:RadicalKISS]`
> - **관련 과제**: `P0-03` (`pkg/storage/memengine/wal.go`) / `ADR-027`

---

## 1. 개요 (The Danger of Over-eager Truncation)

데이터베이스 엔진에서 **WAL(Write-Ahead Log)**은 트랜잭션의 영속성(Durability)을 담보하는 최후의 보루입니다. 메모리 테이블이 아직 체크포인트 디스크에 내려앉지 않았더라도, WAL에 기록된 LSN(Log Sequence Number) 스트림만 온전하다면 서버가 급작스럽게 전원 차단(`kill -9`, 하드웨어 단전)을 겪더라도 100% 무손실 복구되어야 합니다.

그러나 기존 LionSQL의 WAL 복구 루프(`wal.go:Replay()`)에는 치명적인 결함이 존재했습니다:
**"WAL을 읽다가 어떤 이유로든 에러(I/O 타임아웃, 디스크 읽기 실패, CRC 불일치)가 발생하면, 파일의 남은 뒷부분을 무조건 `file.Truncate(validOffset)`로 잘라버리는 동작"**이었습니다.

이는 단순한 디스크 지연이나 일시적 읽기 오류였음에도 불구하고, **정상적으로 커밋 완료된 후속 수만 건의 트랜잭션을 영구 삭제(Silent Data Loss)해버리는 재앙**을 유발했습니다.

---

## 2. 결함의 본질과 재현 시나리오

### 2.1. 기존 취약 코드
```go
// 기존 결함 코드 (wal.go)
for {
    rec, err := r.ReadRecord()
    if err != nil {
        if err == io.EOF {
            break
        }
        // [치명적 결함]: 에러 원인이나 파일 오프셋 위치를 전혀 검증하지 않고
        // 뒤쪽 정상 커밋 트랜잭션을 영구 삭제함!
        _ = r.file.Truncate(r.lastValidOffset)
        break
    }
    apply(rec)
}
```

### 2.2. 발생 가능한 파멸적 시나리오
1. **OS 디스크 I/O 순간 에러 (`syscall.EIO`)**: OS 버퍼 일시 오류로 1개 레코드를 못 읽었을 뿐인데, 뒤에 남아있던 수만 건의 커밋 레코드가 디스크에서 절삭되어 증발.
2. **중간 블록 비트 플립 (Bit Flip) / 단일 손상**: 파일 중간(예: 오프셋 1MB 지점, 전체 파일은 100MB)에서 1개 레코드 손상 시, 99MB에 달하는 정상 데이터가 전부 삭제됨.
3. **권한 및 안티바이러스 프로세스 락 경합**: 복구 중 다른 보안 데몬이 파일을 일시 점유했을 때 읽기 실패를 감지하자마자 멀쩡한 DB를 반파시킴.

---

## 3. 해결책: 4-State MECE Fail-Closed 복구 아키텍처

우리는 `[Invariant 9]`와 `[RULE:ExplicitError]`에 입각하여, 파일 복구 상태를 상호 배타적이고 완전하게 망라(MECE)하는 **4-State 복구 분류 모델**을 수립했습니다:

```
[WAL Recovery Stream Evaluation]
       │
       ├── Case 1: stat.Size() == validOffset (Clean EOF)
       │    └──> [정상 완료] 모든 트랜잭션이 깨끗하게 재생됨. Truncate 불필요.
       │
       ├── Case 2: stat.Size() == validOffset + tornBytes (Verified Torn Write at Tail)
       │    └──> [조건부 절삭 허용] 전원 차단으로 물리적 파일 끝에 기록되다 만 미완성 프레임.
       │         오직 물리적 파일 끝임이 증명된 경우에만 테일 찌꺼기 Truncate 허용.
       │
       ├── Case 3: stat.Size() > validOffset + remainingBytes (Corrupted Middle Block)
       │    └──> [Fail-Closed 즉시 중단] 중간 블록 손상 뒤에 정상 레코드가 존재할 수 있음.
       │         절대 Truncate하지 않고 명시적 에러 진단과 함께 기동을 즉시 멈춤.
       │
       └── Case 4: err is syscall.EIO or OS Read Error (Hardware/OS Transient Failure)
            └──> [Fail-Closed 즉시 중단] 하드웨어/OS 장애 시 쓰기/절삭 시도 원천 차단.
```

---

## 4. 핵심 코드 구현 (`pkg/storage/memengine/wal.go`)

```go
// [Invariant 9 & RULE:ExplicitError] Fail-Closed WAL Recovery with Verified Tail Truncation Only
stat, err := file.Stat()
if err != nil {
    return fmt.Errorf("failed to stat WAL file during recovery: %w", err)
}

fileSize := stat.Size()
for {
    rec, bytesRead, err := readFrame(r)
    if err != nil {
        if errors.Is(err, io.EOF) {
            // Case 1: Clean EOF at physical end of file
            break
        }

        // Verify if this is truly a torn write at the physical tail
        isPhysicalTail := (validOffset + int64(bytesRead)) == fileSize

        if errors.Is(err, io.ErrUnexpectedEOF) && isPhysicalTail {
            // Case 2: Verified partial frame at the absolute tail caused by sudden power loss
            log.Printf("[WAL-RECOVERY] Torn write detected at tail (%d bytes). Safely truncating to offset %d", bytesRead, validOffset)
            if truncErr := file.Truncate(validOffset); truncErr != nil {
                return fmt.Errorf("failed to truncate partial torn write at tail: %w", truncErr)
            }
            break
        }

        // Case 3 & Case 4: Mid-file corruption or OS read error. Fail-Closed!
        return fmt.Errorf("[CRITICAL] unrecoverable WAL read error at offset %d (file size %d): %w. Engine halting to prevent data loss",
            validOffset, fileSize, err)
    }

    validOffset += int64(bytesRead)
    if err := applyRecord(rec); err != nil {
        return fmt.Errorf("failed to apply WAL record at offset %d: %w", validOffset, err)
    }
}
```

---

## 5. 엔지니어링 검증 및 결론

1. **테일 절삭 보존**: 정전 모의 테스트에서 전원 차단 시 발생한 테일의 불완전 프레임은 안전하게 정리되어 정상 복구 완료.
2. **중간 블록 보존**: 파일 중간의 1바이트 변조 시, 절삭을 시도하지 않고 즉각 엔진 기동을 중단하여 수만 건의 후속 트랜잭션을 디스크에 온전히 보존.
3. **불변식 확립**: 본 아키텍처는 LionSQL의 핵심 설계 불변식인 `Invariant 9`로 공식 채택되어 영구 보존됩니다.

# LionSQL 트러블슈팅, 에러 코드 & SQLSTATE 사전 (`doc/TROUBLESHOOTING.ko.md`)

> - **최초 작성일**: 2026-09-22
> - **최종 갱신일**: 2026-11-12
> - **English**: [doc/TROUBLESHOOTING.md](TROUBLESHOOTING.md)

---

이 문서는 LionSQL 운영 및 애플리케이션 연동 중 마주칠 수 있는 시스템 시작 장애, 네트워크 연결 오류, 그리고 PostgreSQL SQLSTATE 및 Microsoft TDS 에러 코드의 원인과 해결 레시피를 일목요연하게 정리한 공식 트러블슈팅 사전입니다.

---

## 1. SQLSTATE 및 TDS 에러 코드 트러블슈팅 사전

### 1.1. `SQLSTATE 40001` / TDS 1205: 직렬화 충돌 (Serialization Failure / Deadlock / Write Conflict)

#### 원인
- **In-Memory OCC 엔진 (`-engine=memory`)**: 두 개 이상의 동시 트랜잭션이 동일한 행(Row)을 읽은 후 거의 동시에 갱신 또는 삭제를 시도할 때 발생합니다. 첫 번째 커밋은 즉시 성공하며, 두 번째 트랜잭션은 ACID 직렬화 무결성을 지키기 위해 유효성 검증 실패(`ErrWriteConflict`)로 롤백됩니다.
- **WiredTiger MVCC 엔진 (`-engine=wiredtiger`)**: 트랜잭션 타임스탬프 충돌 또는 핫스팟 행 동시 쓰기 시 `WT_ROLLBACK`이 발생합니다.

#### 해결 레시피
지수 백오프(Exponential Backoff)를 포함한 애플리케이션 레벨 재시도 루프를 적용합니다:

```go
func runWithRetry(db *sql.DB, maxRetries int, fn func(tx *sql.Tx) error) error {
    var err error
    for attempt := 0; attempt < maxRetries; attempt++ {
        tx, err := db.Begin()
        if err != nil {
            return err
        }
        err = fn(tx)
        if err == nil {
            return tx.Commit()
        }
        _ = tx.Rollback()
        
        // SQLSTATE 40001 직렬화 실패 감지 시 지수 백오프 재시도
        if strings.Contains(err.Error(), "40001") || strings.Contains(err.Error(), "serialization failure") {
            time.Sleep(time.Duration(10*(1<<attempt)) * time.Millisecond)
            continue
        }
        return err
    }
    return err
}
```

---

### 1.2. `SQLSTATE 23505` / TDS 2627: 고유 키 제약조건 위반 (Unique Key / Primary Key Violation)

#### 원인
기본키(PRIMARY KEY) 또는 UNIQUE 인덱스가 설정된 컬럼에 이미 존재하는 값을 중복 삽입(`INSERT`)하려 했습니다.

#### 해결 레시피
1. **PostgreSQL 호환 문법**: `ON CONFLICT DO UPDATE` 또는 `ON CONFLICT DO NOTHING` 사용:
   ```sql
   INSERT INTO accounts (id, balance) VALUES (1, 1000)
   ON CONFLICT (id) DO UPDATE SET balance = accounts.balance + EXCLUDED.balance;
   ```
2. **T-SQL 표준 문법**: `MERGE` 문 또는 사전 존재 여부 확인:
   ```sql
   IF NOT EXISTS (SELECT 1 FROM accounts WHERE id = @id)
   BEGIN
       INSERT INTO accounts (id, balance) VALUES (@id, @bal);
   END;
   ```

---

### 1.3. `SQLSTATE 42P01` / TDS 208: 테이블 또는 뷰를 찾을 수 없음 (Undefined Table / Object)

#### 원인
- 쿼리에서 참조한 테이블명이 실제 카탈로그에 존재하지 않음.
- 대소문자 또는 스키마 접두사(`dbo.`, `public.`) 차이.

#### 해결 레시피
1. 카탈로그에 실제 테이블이 생성되어 있는지 확인:
   ```sql
   SELECT name, type_desc FROM sys.objects WHERE type IN ('U', 'V');
   ```
2. LionSQL은 단일 인스턴스 단일 카탈로그 격리를 적용하므로, 외부 데이터베이스명을 참조하는 경우 접두사를 정리하십시오 (`[master].[dbo].[table]`은 지원됨).

---

### 1.4. `SQLSTATE 23503` / TDS 547: 외래키 제약조건 위반 (Foreign Key Constraint Violation)

#### 원인
부모 테이블에 존재하지 않는 외래키 ID를 자식 테이블에 삽입하려 하거나, 자식 테이블에 참조 행이 남아있는 상태에서 부모 테이블의 행을 삭제하려 했습니다.

#### 해결 레시피
- 데이터 삽입 순서 검증 (부모 테이블 ➔ 자식 테이블 순서로 삽입).
- 외래키 선언 시 `ON DELETE CASCADE` 지정 검토.

---

### 1.5. `SQLSTATE 22012` / TDS 8134: 분모 0 오류 (Divide by Zero)

#### 원인
산술 연산식에서 분모가 `0`인 나눗셈(`x / 0`)을 실행했습니다.

#### 해결 레시피
`NULLIF` 함수를 사용하여 분모가 0일 때 `NULL`로 안전하게 변환:
```sql
SELECT amount / NULLIF(total, 0) AS ratio FROM sales;
```

---

### 1.6. `SQLSTATE 53300`: 세션 풀 고갈 (Too Many Connections / Pool Exhausted)

#### 원인
동시 클라이언트 연결 수가 LionSQL 내장 커넥션 풀러 상한(`-max-workers`, `-max-idle-sessions`)을 초과하여 대기 큐 타임아웃이 발생했습니다.

#### 해결 레시피
서버 구동 플래그에서 워커 및 유휴 세션 풀 크기 확장:
```bash
./bin/lionsql -max-workers=64 -max-idle-sessions=128
```

---

## 2. 서버 구동 및 포트 바인딩 장애

### 2.1. `bind: address already in use`

#### 증상
```
Failed to start PostgreSQL wire protocol listener: listen tcp 127.0.0.1:5433: bind: address already in use
```

#### 해결 방법
1. 포트를 점유하고 있는 프로세스 확인:
   ```bash
   lsof -i :5433
   lsof -i :1433
   lsof -i :8090
   ```
2. 기존 프로세스 종료 또는 대체 포트로 구동:
   ```bash
   ./bin/lionsql -pg=127.0.0.1:5434 -tds=127.0.0.1:1434 -http=127.0.0.1:8091
   ```

---

### 2.2. `wiredtiger_open failed`

#### 증상
```
[LionSQL] Failed to open WiredTiger storage engine: wiredtiger_open failed with code 2: No such file or directory
```

#### 해결 방법
1. 데이터 저장 디렉터리 권한 확인:
   ```bash
   mkdir -p data && chmod 755 data
   ```
2. 네이티브 라이브러리가 없는 환경인 경우 Pure-Go 인메모리 엔진으로 전환:
   ```bash
   ./bin/lionsql -engine=memory -dir=data
   ```

---

## 3. 쿼리 성능 및 메모리 거버너 진단

### 3.1. 느린 쿼리 (Slow Query) 진단
1. `EXPLAIN ANALYZE <sql>`를 실행하여 각 파이프라인 단계별 실제 소요 시간(Wall-clock)과 반환 행 수 확인.
2. 테이블 통계가 미수집된 경우 `ANALYZE TABLE <테이블명>;`을 실행하여 CBO 옵티마이저가 최적의 인덱스 스캔을 선택하도록 갱신.
3. 정렬이나 해시 조인이 디스크로 스필되는 경우 `SET work_mem = '128MB';`로 작업 메모리 상향 조정.

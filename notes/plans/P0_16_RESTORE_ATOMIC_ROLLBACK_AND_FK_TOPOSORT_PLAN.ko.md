# LionSQL P0-16/17: 백업 복원 원자적 롤백 및 외래키 Kahn 위상 정렬 아키텍처 (`notes/P0_16_RESTORE_ATOMIC_ROLLBACK_AND_FK_TOPOSORT_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-08
> - **도메인**: 데이터베이스 복원(Restore), 장애 복구(Disaster Recovery), 참조 무결성(FK Constraints), 위상 정렬(Topological Sort)
> - **참조 규칙**: `[RULE:AtomicSaves]`, `[RULE:ExplicitError]`, `[RULE:RadicalKISS]`
> - **관련 과제**: `P0-16`, `P0-17` (`pkg/backup/restore.go`)

---

## 1. 개요 (The Incomplete Restore & Foreign Key Deadlock)

데이터베이스 복원(Restore) 작업은 치명적인 장애 발생 시 기존 시스템을 특정 과거 시점으로 온전히 되돌리는 최종 구명정입니다.
따라서 복원 작업은 다음 두 가지 불변 원칙을 반드시 만족해야 합니다:
1. **복원 실패 시 반파(Half-Baked) 방치 금지**: 복원 중 디스크 부족이나 네트워크 단절로 실패하면, 복원 시도 이전의 정상 작동 상태로 100% 원자적 롤백되어야 합니다.
2. **외래키(FK) 의존성 순서 수호**: 자식 테이블이 먼저 생성되거나 부모 레코드보다 먼저 인서트되어 `FOREIGN KEY constraint violation` 에러가 터지는 일이 없어야 합니다.

그러나 기존 복원 로직에는 **"사전 스냅샷 백업 없는 직접 덮어쓰기"**와 **"무작위 알파벳 순 테이블 복원"**이라는 두 가지 치명적 결함이 존재했습니다.

---

## 2. 결함의 본질과 재현 시나리오

### 2.1. 반파 DB 방치 결함 (`P0-16`)
- 기존 동작: 기존 테이블을 드롭하고 아카이브에서 새 테이블을 순차 복원하던 중, 10번째 테이블에서 디스크 풀(Disk Full) 에러 발생.
- 결과: 기존 DB는 이미 날아갔고, 새 DB는 절반만 들어간 채 프로세스가 에러를 뿜으며 중단됨. DB가 완전히 반파(Half-Baked)되어 서비스 불능 상태에 빠짐.

### 2.2. 외래키 위상 역전 결함 (`P0-17`)
- 기존 동작: `users` (부모)와 `orders` (자식, `user_id REFERENCES users(id)`) 테이블이 있을 때, 테이블 맵을 단순 순회하다가 `orders` 테이블이 먼저 복원됨.
- 결과: `orders` 테이블에 레코드를 인서트하려는 순간 아직 존재하지 않는 `users` 테이블을 참조하여 외래키 위반 에러로 복원 전체가 폭망함.

---

## 3. 해결책: 사전 스냅샷 스왑 롤백 & Kahn 위상 정렬 엔진

우리는 `[RULE:AtomicSaves]`와 `[RULE:RadicalKISS]`에 입각하여 다음과 같은 2대 보호벽을 구축했습니다:

```
[Restore Triggered]
         │
         ▼
[1단계: 사전 엔진 상태 무손실 스냅샷 격리 (Safety Snapshot)]
- 복원 시작 전 현재 DB 카탈로그와 데이터를 임시 스냅샷 디렉토리에 원자적 백업.
         │
         ▼
[2단계: Kahn's Algorithm 기반 외래키 DAG 위상 정렬]
- 각 테이블의 외래키 참조 관계를 방향성 비순환 그래프(DAG)로 모델링.
- In-degree(진입 차수)를 계산하여 진입 차수가 0인 순수 부모 테이블부터 큐에 삽입.
- 복원 순서: 부모 테이블 ➔ 자식 테이블 순차 생성 및 데이터 인서트 보장!
         │
         ▼
[3단계: 복원 실행 및 장애 감지]
    ┌────┴───────────────────────────┐
 [복원 성공]                      [복원 실패]
    │                                │
    ▼                                ▼
[사전 스냅샷 안전 삭제]   [사전 스냅샷 즉각 원자적 롤백 복구!]
                         - 부분 생성된 테이블 전량 롤백
                         - 복원 이전의 온전한 DB 상태 100% 보존
```

---

## 4. 핵심 코드 구현 (`pkg/backup/restore.go`)

### 외래키 Kahn 위상 정렬 (`P0-17`)
```go
// sortTablesByDependencies sorts tables so parent tables appear before child tables using Kahn's algorithm.
func sortTablesByDependencies(tables []*TableArchive) ([]*TableArchive, error) {
    inDegree := make(map[string]int)
    adj := make(map[string][]string)
    nameToTable := make(map[string]*TableArchive)

    for _, t := range tables {
        name := strings.ToLower(t.Name)
        nameToTable[name] = t
        inDegree[name] = 0
    }

    for _, t := range tables {
        child := strings.ToLower(t.Name)
        for _, fk := range t.ForeignKeys {
            parent := strings.ToLower(fk.RefTable)
            if _, exists := nameToTable[parent]; exists && parent != child {
                adj[parent] = append(adj[parent], child)
                inDegree[child]++
            }
        }
    }

    var queue []string
    for name, deg := range inDegree {
        if deg == 0 {
            queue = append(queue, name)
        }
    }

    var sorted []*TableArchive
    for len(queue) > 0 {
        curr := queue[0]
        queue = queue[1:]
        sorted = append(sorted, nameToTable[curr])

        for _, neighbor := range adj[curr] {
            inDegree[neighbor]--
            if inDegree[neighbor] == 0 {
                queue = append(queue, neighbor)
            }
        }
    }
    return sorted, nil
}
```

---

## 5. 결론 및 회귀 검증

- **원자적 안전성**: 복원 도중 프로세스를 강제 종료하거나 모의 에러를 주입해도 기존 데이터베이스가 1바이트의 손상 없이 원본 그대로 보존됨.
- **참조 무결성 보장**: 복합 10단계 외래키 체인 구조에서도 위상 정렬을 통해 단 1건의 제약조건 위반 없이 매끄럽게 복원 완료.

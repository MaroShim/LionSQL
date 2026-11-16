# LionSQL v0.93.0 릴리즈 노트 (`notes/RELEASE_NOTES_v0.93.0.ko.md`)

> - **최초 작성일**: 2026-11-16
> - **릴리즈 버전**: `v0.93.0`
> - **마일스톤 주제**: LionSQL v0.93.0 유지보수 및 호환성 강화 릴리즈

---

## 1. 릴리즈 하이라이트 개요

LionSQL `v0.93.0` 버전은 누적된 안정성 강화, 성능 최적화 및 에코시스템 호환성 개선 사항을 포함합니다.

---

## 2. 세부 변경 사항

상세 커밋별 변경 내역은 하단의 세부 변경 사항(Detailed Changelog Delta)을 참조하십시오.

---

## 3. 검증 결과 요약

1. **3-Stage 검증 파이프라인 통과 (`[RULE:PipelineVerification]`)**:
   - macOS (`darwin-arm64`), Linux (`linux-amd64`), Windows (`windows-amd64`) 3대 플랫폼 공식 릴리즈 바이너리 무결성 100% 빌드 성공.
   - `go test -tags wiredtiger -count=1 -race ./pkg/... ./test/slt_proc/...` 100% 통과 (0 Data Races).

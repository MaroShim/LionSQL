# Casdoor + LionSQL 듀얼 프로토콜 연동 가이드 (`PostgreSQL` & `MSSQL`)

> - **최초 작성일**: 2026-10-31
> - **대상 버전**: LionSQL v1.0.x / Casdoor (Beego + XORM + Casbin)
> - **지원 프로토콜**: PostgreSQL Wire Protocol v3 (`:5433`), Microsoft SQL Server TDS 7.x/8.0 (`:1433`)

---

## 1. 개요

**Casdoor**(`github.com/casdoor/casdoor`)는 OAuth 2.0, OIDC(OpenID Connect), SAML, CAS, LDAP, RADIUS를 지원하는 대표적인 오픈소스 통합 인증(IAM / SSO) 플랫폼입니다.
LionSQL은 단일 바이너리 데이터베이스 엔진으로서 Casdoor의 **XORM + Casbin** 영속성 계층을 코드 수정 없이 **PostgreSQL 프로토콜(`:5433`)** 과 **Microsoft SQL Server TDS 프로토콜(`:1433`)** 양쪽 모두에서 완벽하게 지원합니다.

---

## 2. LionSQL 서버 기동

```bash
./bin/lionsql -data-dir=data_casdoor -admin-pass=1234567890
```

- **PostgreSQL Wire 리스너**: `127.0.0.1:5433`
- **MSSQL TDS 리스너**: `127.0.0.1:1433`
- **Web Studio**: `http://127.0.0.1:8090/_/`

---

## 3. Casdoor 설정 (`conf/app.conf`)

### Mode A: PostgreSQL Wire Protocol (`:5433`) 연동

`conf/app.conf`에서 `driverName`과 `dataSourceName`을 아래와 같이 구성합니다:

```ini
appname = casdoor
httpport = 8000
runmode = dev
copyrequestbody = true
driverName = postgres
dataSourceName = "user=admin password=1234567890 host=127.0.0.1 port=5433 sslmode=disable dbname=casdoor"
dbName =
tableNamePrefix =
showSql = false
initAdminPassword = "123"
```

### Mode B: Microsoft SQL Server TDS (`:1433`) 연동

`conf/app.conf`에서 `driverName = mssql`로 구성합니다:

```ini
appname = casdoor
httpport = 8000
runmode = dev
copyrequestbody = true
driverName = mssql
dataSourceName = "sqlserver://admin:1234567890@127.0.0.1:1433?database=casdoor&encrypt=disable"
dbName = casdoor
tableNamePrefix =
showSql = false
initAdminPassword = "123"
```

---

## 4. Casdoor 실행 및 동작 확인

```bash
# Casdoor 백엔드 기동 (최초 기동 시 44개 테이블 및 기본 조직/관리자/인증서 자동 생성)
./bin/casdoor
```

### 4.1 OIDC Discovery 및 JWKS 확인

```bash
curl -s http://127.0.0.1:8000/.well-known/openid-configuration | jq .
curl -s http://127.0.0.1:8000/.well-known/jwks | jq .
```

### 4.2 관리자 로그인 및 조직/애플리케이션/사용자 생성 API 검증

```bash
# 1. built-in/admin 로그인
curl -s -c /tmp/casdoor_cookie.txt -X POST http://127.0.0.1:8000/api/login \
  -H "Content-Type: application/json" \
  -d '{"application":"app-built-in","organization":"built-in","username":"admin","password":"123","autoSignin":true,"type":"login"}'

# 2. 현재 로그인 계정 정보 조회
curl -s -b /tmp/casdoor_cookie.txt "http://127.0.0.1:8000/api/get-account" | jq .

# 3. 신규 조직(Organization) 생성
curl -s -b /tmp/casdoor_cookie.txt -X POST http://127.0.0.1:8000/api/add-organization \
  -H "Content-Type: application/json" \
  -d '{"owner":"admin","name":"lionsql-org","displayName":"LionSQL Organization","websiteUrl":"https://lionsql.io","passwordType":"plain"}'

# 4. 신규 애플리케이션(Application) 생성
curl -s -b /tmp/casdoor_cookie.txt -X POST http://127.0.0.1:8000/api/add-application \
  -H "Content-Type: application/json" \
  -d '{"owner":"admin","name":"lionsql-app","displayName":"LionSQL App","organization":"lionsql-org","cert":"cert-built-in","enablePassword":true}'

# 5. 신규 사용자(User) 생성 및 조회
curl -s -b /tmp/casdoor_cookie.txt -X POST http://127.0.0.1:8000/api/add-user \
  -H "Content-Type: application/json" \
  -d '{"owner":"lionsql-org","name":"lionsql-user","displayName":"LionSQL User","email":"user@lionsql.io","password":"123","type":"normal-user","signupApplication":"lionsql-app"}'

curl -s -b /tmp/casdoor_cookie.txt "http://127.0.0.1:8000/api/get-user?id=lionsql-org/lionsql-user" | jq .
```

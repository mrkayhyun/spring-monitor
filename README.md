# spring-monitor (sm)

> 리눅스에서 Spring Boot 애플리케이션을 관리하는 k9s 스타일 TUI CLI

`sm` 하나로 실행 중인 Spring Boot 서비스를 한눈에 확인하고, 로그를 보고, 종료까지 할 수 있습니다.
더 이상 폴더 이동하며 로그 찾고, ps 치고, kill 명령 일일이 입력할 필요 없습니다.

> English version: [README.en.md](README.en.md)

---

## 화면 구성

```
 spring-monitor v0.1  │  q:종료  l:로그  K:종료  d:상세  r:새로고침  ↑↓jk:이동    15:04:05
  NAME                     PID     PORT(S)        UPTIME     MEM(MB)  ACTUATOR
 ▶ user-service            12345   8080           2h35m      256      ✓  :8080
   payment-service         23456   8081           45m        128      ✗
   auth-service            34567   8082,9090      1d3h       512      ✓  :9090

 3개의 Spring 앱 실행 중
```

---

## 단축키

### 메인 리스트

| 키 | 동작 |
|----|------|
| `↑` / `k` | 위로 이동 |
| `↓` / `j` | 아래로 이동 |
| `l` / Enter | 로그 뷰어 열기 |
| `K` | 프로세스 종료 다이얼로그 |
| `d` | 상세 정보 (Actuator 포함) |
| `r` | 수동 새로고침 |
| `q` | 종료 |

### 로그 뷰어

| 키 | 동작 |
|----|------|
| `f` | Follow 모드 토글 (tail -f) |
| `↑` / `k` | 위로 스크롤 |
| `↓` / `j` | 아래로 스크롤 |
| `PgUp` | 페이지 위 |
| `PgDn` | 페이지 아래 |
| `g` | 맨 위로 |
| `G` | 맨 아래로 |
| `q` / ESC | 목록으로 돌아가기 |

### 종료 다이얼로그

| 키 | 동작 |
|----|------|
| `g` | Actuator Graceful Shutdown (`POST /actuator/shutdown`) |
| `t` | SIGTERM 전송 (Spring graceful shutdown 설정 시 동작) |
| `K` | SIGKILL 강제 종료 |
| ESC / `c` | 취소 |

---

## 설치

### 소스 빌드

```bash
git clone git@github.com:mrkayhyun/spring-monitor.git
cd spring-monitor
make build-linux          # Linux amd64 바이너리 빌드
make install              # /usr/local/bin/sm 으로 설치
```

### 요구사항

- **실행 환경**: Linux (`/proc` 파일시스템 사용)
- **빌드 환경**: Go 1.21+

---

## 동작 원리

### 프로세스 감지

`/proc/*/cmdline`을 스캔하여 아래 조건에 맞는 Java 프로세스를 Spring 앱으로 판별합니다.

- `-jar` 플래그 또는 `-Dspring.*` JVM 인수 포함
- `-classpath`에 `org.springframework` 관련 jar 포함

앱 이름 추출 우선순위:

1. `-Dspring.application.name=<이름>` JVM 인수
2. `-jar <파일.jar>` → jar 파일명에서 버전 제거
3. `-classpath .../target/classes` → 프로젝트 디렉터리명 (IntelliJ/Maven exec 실행 시)
4. 메인 클래스명 → `com.example.FooApplication` → `foo`

### 포트 감지

`/proc/net/tcp` 및 `/proc/net/tcp6`에서 LISTEN 소켓의 inode를 읽고,
`/proc/<pid>/fd/` 심볼릭 링크와 매칭하여 각 프로세스가 사용하는 포트를 정확히 식별합니다.

### Actuator 연동

감지된 포트로 `GET /actuator`를 호출합니다.

- 응답 성공 시 `✓` (Actuator 활성)
- `/actuator/shutdown` 엔드포인트 존재 여부 확인
- `/actuator/health`로 헬스 상태 표시

### 로그 파일 탐색

다음 순서로 로그 파일을 찾습니다.

1. `-Dlogging.file.name=` JVM 인수
2. `<실행디렉터리>/logs/<앱이름>.log`
3. `<실행디렉터리>/logs/application.log`
4. `/var/log/<앱이름>/application.log`

---

## Spring Boot 권장 설정

`sm`을 최대한 활용하려면 아래 설정을 추가하세요.

```yaml
# application.yml
spring:
  application:
    name: my-service          # sm에서 표시되는 앱 이름

logging:
  file:
    name: /var/log/my-service/application.log   # 로그 파일 경로

management:
  server:
    port: 9090                # Actuator 전용 포트 (선택)
  endpoints:
    web:
      exposure:
        include: health,info,shutdown
  endpoint:
    shutdown:
      enabled: true           # Graceful shutdown 엔드포인트 활성화

server:
  shutdown: graceful          # SIGTERM 시 graceful shutdown
```

---

## 로드맵

- [ ] Actuator 메트릭 대시보드 (heap, thread, HTTP 통계)
- [ ] 헬스 체크 자동 갱신 표시
- [ ] SSH 원격 서버 모니터링
- [ ] systemd 연동 (`journalctl` 로그 지원)
- [ ] 멀티 프로세스 로그 통합 뷰
- [ ] 로그 경로 커스텀 패턴 설정
- [ ] 플러그인 시스템

---

## 기여

이슈 및 PR 환영합니다 → [github.com/mrkayhyun/spring-monitor](https://github.com/mrkayhyun/spring-monitor)

---

## 라이선스

MIT

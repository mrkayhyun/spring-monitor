# spring-monitor (sm)

> A k9s-inspired TUI for managing Spring Boot applications on Linux

`sm` lets you monitor, inspect, and control all your Spring Boot services from a single terminal interface — no more jumping between directories, tailing logs, or running `kill` commands manually.

> 한국어 버전: [README.md](README.md)

---

## Screenshot

```
 spring-monitor v0.1  │  q:quit  l:logs  K:kill  d:describe  r:refresh  ↑↓jk:nav    15:04:05
  NAME                     PID     PORT(S)        UPTIME     MEM(MB)  ACTUATOR
 ▶ user-service            12345   8080           2h35m      256      ✓  :8080
   payment-service         23456   8081           45m        128      ✗
   auth-service            34567   8082,9090      1d3h       512      ✓  :9090

 3 Spring app(s) running
```

---

## Keybindings

### Main List

| Key | Action |
|-----|--------|
| `↑` / `k` | Navigate up |
| `↓` / `j` | Navigate down |
| `l` / Enter | Open log viewer |
| `K` | Kill dialog |
| `d` | Describe process + actuator info |
| `r` | Manual refresh |
| `q` | Quit |

### Log Viewer

| Key | Action |
|-----|--------|
| `f` | Toggle follow mode (tail -f) |
| `↑` / `k` | Scroll up |
| `↓` / `j` | Scroll down |
| `PgUp` | Page up |
| `PgDn` | Page down |
| `g` | Go to top |
| `G` | Go to bottom |
| `q` / ESC | Back to list |

### Kill Dialog

| Key | Action |
|-----|--------|
| `g` | Graceful shutdown via Actuator (`POST /actuator/shutdown`) |
| `t` | Send SIGTERM (graceful if `server.shutdown=graceful`) |
| `K` | Force SIGKILL |
| ESC / `c` | Cancel |

---

## Installation

### Build from source

```bash
git clone git@github.com:mrkayhyun/spring-monitor.git
cd spring-monitor
make build-linux          # Linux amd64
make install              # copies to /usr/local/bin/sm
```

### Requirements

- **Runtime**: Linux (`/proc` filesystem)
- **Build**: Go 1.21+

---

## How it Works

### Process Detection

`sm` scans `/proc/*/cmdline` for Java processes matching Spring indicators:

- Contains `-jar` flag or `-Dspring.*` JVM arguments
- `-classpath` includes `org.springframework` jars

App name resolution priority:

1. `-Dspring.application.name=<name>` JVM argument
2. `-jar <file.jar>` → jar filename with version stripped
3. `-classpath .../target/classes` → project directory name (IntelliJ / Maven exec)
4. Main class argument → `com.example.FooApplication` → `foo`

### Port Detection

Reads LISTEN socket inodes from `/proc/net/tcp` and `/proc/net/tcp6`, then matches them against `/proc/<pid>/fd/` symlinks to accurately map ports to each process.

### Actuator Integration

Probes `GET /actuator` on the detected management port:

- Returns `✓` if Actuator is reachable
- Checks for `/actuator/shutdown` endpoint availability
- Shows health status from `/actuator/health`

### Log File Discovery

Log files are located in this order:

1. `-Dlogging.file.name=` JVM argument
2. `<working-dir>/logs/<name>.log`
3. `<working-dir>/logs/application.log`
4. `/var/log/<name>/application.log`

---

## Recommended Spring Boot Configuration

```yaml
# application.yml
spring:
  application:
    name: my-service          # displayed in sm

logging:
  file:
    name: /var/log/my-service/application.log

management:
  server:
    port: 9090                # dedicated actuator port (optional)
  endpoints:
    web:
      exposure:
        include: health,info,shutdown
  endpoint:
    shutdown:
      enabled: true           # enables graceful shutdown endpoint

server:
  shutdown: graceful          # graceful shutdown on SIGTERM
```

---

## Roadmap

- [ ] Actuator metrics dashboard (heap, threads, HTTP stats)
- [ ] Health check indicator with auto-refresh
- [ ] SSH remote monitoring
- [ ] systemd integration (`journalctl` log support)
- [ ] Multi-process log aggregation view
- [ ] Custom log path pattern configuration
- [ ] Plugin system

---

## Contributing

Issues and PRs welcome at [github.com/mrkayhyun/spring-monitor](https://github.com/mrkayhyun/spring-monitor)

---

## License

MIT

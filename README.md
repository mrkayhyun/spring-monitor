# spring-monitor (sm)

> A k9s-inspired TUI for managing Spring Boot applications on Linux

`sm` lets you monitor, inspect, and control your Spring Boot services from a single terminal interface — no more jumping between directories, tailing logs, or manually running `kill`.

---

## Screenshot

```
 spring-monitor dev  │  q:quit  l:logs  K:kill  d:describe  r:refresh  ↑↓jk:nav    15:04:05
  NAME                     PID     PORT(S)        UPTIME     MEM(MB)  ACTUATOR
 ▶ user-service            12345   8080           2h35m      256      ✓  :8080
   payment-service         23456   8081           45m        128      ✗
   auth-service            34567   8082,9090       1d3h      512      ✓  :9090

 3 Spring app(s) running
```

---

## Features

| Key       | Action                                      |
|-----------|---------------------------------------------|
| `↑` / `k` | Navigate up                                 |
| `↓` / `j` | Navigate down                               |
| `l` / Enter | Open log viewer                           |
| `K`       | Kill dialog (graceful / SIGTERM / SIGKILL)  |
| `d`       | Describe process + actuator info            |
| `r`       | Manual refresh                              |
| `q`       | Quit                                        |

### Log Viewer

| Key       | Action         |
|-----------|----------------|
| `f`       | Toggle follow mode (tail -f) |
| `↑` / `k` | Scroll up      |
| `↓` / `j` | Scroll down    |
| `PgUp`    | Page up        |
| `PgDn`    | Page down      |
| `g`       | Go to top      |
| `G`       | Go to bottom   |
| `q` / ESC | Back to list   |

### Kill Dialog

1. **`g`** — Graceful shutdown via Spring Actuator (`POST /actuator/shutdown`)
2. **`t`** — Send `SIGTERM` (Spring handles graceful shutdown if configured)
3. **`K`** — Force `SIGKILL` (immediate, no cleanup)

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

- Linux (uses `/proc` filesystem for process detection)
- Go 1.21+ (for building)

---

## How it Works

### Process Detection

`sm` scans `/proc/*/cmdline` for Java processes with Spring indicators (`-jar`, `-Dspring.*`, `org.springframework`). It then:

1. Reads `/proc/net/tcp` and `/proc/net/tcp6` for listening socket inodes
2. Matches inodes via `/proc/<pid>/fd/` symlinks to find which ports belong to each process
3. Parses JVM arguments to extract:
   - App name (`-Dspring.application.name`)
   - Log file path (`-Dlogging.file.name`)
   - Management port (`-Dmanagement.server.port`)

### Actuator Integration

`sm` probes `GET /actuator` on the detected management port. If available, it:
- Lists available endpoints
- Checks if `/actuator/shutdown` is enabled
- Shows health status from `/actuator/health`

### Log File Discovery

Log files are found by checking (in order):
1. `-Dlogging.file.name` JVM argument
2. `<working-dir>/logs/<name>.log`
3. `<working-dir>/logs/application.log`
4. `/var/log/<name>/application.log`

---

## Configuration (Spring Boot)

To get the most out of `sm`, configure your Spring Boot app:

```yaml
# application.yml
spring:
  application:
    name: my-service

logging:
  file:
    name: /var/log/my-service/application.log

management:
  server:
    port: 9090
  endpoints:
    web:
      exposure:
        include: health,info,shutdown
  endpoint:
    shutdown:
      enabled: true

server:
  shutdown: graceful   # enable graceful shutdown on SIGTERM
```

---

## Roadmap

- [ ] Actuator metrics dashboard (heap, threads, HTTP stats)
- [ ] Health check indicator with auto-refresh
- [ ] SSH remote monitoring
- [ ] systemd service integration (`journalctl`)
- [ ] Multi-process log aggregation
- [ ] Config file for custom log path patterns
- [ ] Plugin system

---

## Contributing

Issues and PRs welcome at [github.com/mrkayhyun/spring-monitor](https://github.com/mrkayhyun/spring-monitor)

---

## License

MIT

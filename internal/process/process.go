package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ActuatorStatus represents Spring Actuator availability
type ActuatorStatus int

const (
	ActuatorUnknown  ActuatorStatus = iota
	ActuatorEnabled
	ActuatorDisabled
)

// SpringProcess represents a running Spring Boot application
type SpringProcess struct {
	PID              int
	Name             string
	Ports            []int
	StartTime        time.Time
	MemoryMB         int64
	LogFile          string
	CmdLine          []string
	WorkingDir       string
	JarFile          string
	ActuatorPort     int
	ActuatorStatus   ActuatorStatus
	ActuatorBasePath string
}

func (p *SpringProcess) Uptime() string {
	if p.StartTime.IsZero() {
		return "N/A"
	}
	d := time.Since(p.StartTime)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

func (p *SpringProcess) PortsString() string {
	if len(p.Ports) == 0 {
		return "-"
	}
	parts := make([]string, len(p.Ports))
	for i, port := range p.Ports {
		parts[i] = strconv.Itoa(port)
	}
	return strings.Join(parts, ",")
}

func (p *SpringProcess) ActuatorURL() string {
	if p.ActuatorPort == 0 {
		return ""
	}
	base := p.ActuatorBasePath
	if base == "" {
		base = "/actuator"
	}
	return fmt.Sprintf("http://localhost:%d%s", p.ActuatorPort, base)
}

// FindLogFile locates the log file for this process
func (p *SpringProcess) FindLogFile() string {
	if p.LogFile != "" {
		if fileExists(p.LogFile) {
			return p.LogFile
		}
	}
	var candidates []string
	if p.WorkingDir != "" {
		candidates = append(candidates,
			filepath.Join(p.WorkingDir, "logs", p.Name+".log"),
			filepath.Join(p.WorkingDir, "logs", "application.log"),
			filepath.Join(p.WorkingDir, "logs", "spring.log"),
			filepath.Join(p.WorkingDir, p.Name+".log"),
			filepath.Join(p.WorkingDir, "application.log"),
		)
	}
	candidates = append(candidates,
		"/var/log/"+p.Name+"/application.log",
		"/var/log/"+p.Name+".log",
		"/opt/"+p.Name+"/logs/application.log",
	)
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	return ""
}

// parseAppName extracts application name from JVM arguments.
// Priority:
//  1. -Dspring.application.name=<name>
//  2. -jar <file.jar>  → jar filename without version suffix
//  3. -classpath / -cp → project dir extracted from .../target/classes path
//  4. Main class arg   → last non-flag dotted token, stripped of "Application"/"App"
func parseAppName(cmdline []string) string {
	// 1. Explicit spring app name
	for _, arg := range cmdline {
		if strings.HasPrefix(arg, "-Dspring.application.name=") {
			return strings.TrimPrefix(arg, "-Dspring.application.name=")
		}
	}

	// 2. -jar <file.jar>
	for _, arg := range cmdline {
		if strings.HasSuffix(arg, ".jar") {
			base := filepath.Base(arg)
			name := strings.TrimSuffix(base, ".jar")
			parts := strings.Split(name, "-")
			var nameParts []string
			for _, p := range parts {
				if len(p) > 0 && p[0] >= '0' && p[0] <= '9' {
					break
				}
				nameParts = append(nameParts, p)
			}
			if len(nameParts) > 0 {
				return strings.Join(nameParts, "-")
			}
			return name
		}
	}

	// 3. -classpath / -cp: find .../target/classes or .../build/classes
	for i, arg := range cmdline {
		if (arg == "-classpath" || arg == "-cp") && i+1 < len(cmdline) {
			if name := nameFromClasspath(cmdline[i+1]); name != "" {
				return name
			}
		}
	}

	// 4. Main class: last non-flag argument with dots (e.g. com.example.MyApplication)
	for i := len(cmdline) - 1; i >= 0; i-- {
		arg := cmdline[i]
		if !strings.HasPrefix(arg, "-") && strings.Contains(arg, ".") {
			parts := strings.Split(arg, ".")
			simple := parts[len(parts)-1]
			simple = strings.TrimSuffix(simple, "Application")
			simple = strings.TrimSuffix(simple, "App")
			if simple != "" {
				return strings.ToLower(simple)
			}
		}
	}

	return "unknown"
}

// nameFromClasspath scans a colon-separated classpath for a project directory.
// Returns the parent folder name of the first .../target/classes or .../build/classes entry.
func nameFromClasspath(cp string) string {
	for _, entry := range strings.Split(cp, ":") {
		for _, marker := range []string{"/target/classes", "/build/classes"} {
			if idx := strings.LastIndex(entry, marker); idx >= 0 {
				return filepath.Base(entry[:idx])
			}
		}
	}
	return ""
}

func parseJarFile(cmdline []string) string {
	for _, arg := range cmdline {
		if strings.HasSuffix(arg, ".jar") {
			return arg
		}
	}
	return ""
}

func parseLogFile(cmdline []string) string {
	for _, arg := range cmdline {
		if strings.HasPrefix(arg, "-Dlogging.file.name=") {
			return strings.TrimPrefix(arg, "-Dlogging.file.name=")
		}
		if strings.HasPrefix(arg, "-Dlogging.file=") {
			return strings.TrimPrefix(arg, "-Dlogging.file=")
		}
	}
	return ""
}

func parseActuatorPort(cmdline []string) int {
	for _, arg := range cmdline {
		if strings.HasPrefix(arg, "-Dmanagement.server.port=") {
			if port, err := strconv.Atoi(strings.TrimPrefix(arg, "-Dmanagement.server.port=")); err == nil {
				return port
			}
		}
	}
	return 0
}

func parseActuatorBasePath(cmdline []string) string {
	for _, arg := range cmdline {
		if strings.HasPrefix(arg, "-Dmanagement.endpoints.web.base-path=") {
			return strings.TrimPrefix(arg, "-Dmanagement.endpoints.web.base-path=")
		}
	}
	return "/actuator"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

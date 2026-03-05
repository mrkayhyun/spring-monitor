package process

import (
	"fmt"
	"net/http"
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

	// Additional info
	Profiles     string // active spring profiles, e.g. "dev,mysql"
	JavaVersion  string // e.g. "11", "17", "21"
	XmxMB        int64  // max heap in MB (0 = not set)
	Threads      int    // thread count
	HealthStatus string // "UP", "DOWN", "OUT_OF_SERVICE", "" = unchecked
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

	// 2. -jar <file.jar>  (must be explicitly preceded by -jar flag)
	for i, arg := range cmdline {
		if arg == "-jar" && i+1 < len(cmdline) {
			base := filepath.Base(cmdline[i+1])
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
	for i, arg := range cmdline {
		if arg == "-jar" && i+1 < len(cmdline) {
			return cmdline[i+1]
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

// parseProfiles extracts active Spring profiles from JVM/Spring Boot arguments
func parseProfiles(cmdline []string) string {
	for _, arg := range cmdline {
		for _, prefix := range []string{"-Dspring.profiles.active=", "--spring.profiles.active="} {
			if strings.HasPrefix(arg, prefix) {
				return strings.TrimPrefix(arg, prefix)
			}
		}
	}
	return ""
}

// parseJavaVersion extracts the Java major version from the JVM binary path
// e.g. .../microsoft-11.jdk/... → "11", .../jdk-17/... → "17"
func parseJavaVersion(cmdline []string) string {
	if len(cmdline) == 0 {
		return ""
	}
	binary := strings.ToLower(cmdline[0])

	// Patterns that precede the version number in common JDK directory names
	for _, marker := range []string{
		"jdk-", "jdk", "java-", "openjdk-", "openjdk",
		"temurin-", "microsoft-", "adoptopenjdk-", "corretto-", "graalvm-",
	} {
		idx := strings.Index(binary, marker)
		if idx < 0 {
			continue
		}
		rest := binary[idx+len(marker):]
		var ver strings.Builder
		for _, r := range rest {
			if r >= '0' && r <= '9' {
				ver.WriteRune(r)
			} else if ver.Len() > 0 {
				break
			}
		}
		// Skip "1.8" style — return "8"
		if ver.String() == "1" {
			continue
		}
		if ver.Len() > 0 {
			return ver.String()
		}
	}
	return ""
}

// parseXmx extracts the max heap size in MB from -Xmx or -XX:MaxHeapSize
func parseXmx(cmdline []string) int64 {
	for _, arg := range cmdline {
		if strings.HasPrefix(arg, "-Xmx") {
			return parseJVMMemory(strings.TrimPrefix(arg, "-Xmx"))
		}
		if strings.HasPrefix(arg, "-XX:MaxHeapSize=") {
			return parseJVMMemory(strings.TrimPrefix(arg, "-XX:MaxHeapSize="))
		}
	}
	return 0
}

// parseJVMMemory converts JVM memory strings like "512m", "2g", "1024k" to MB
func parseJVMMemory(s string) int64 {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 0 {
		return 0
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0
	}
	switch unit {
	case 'g':
		return n * 1024
	case 'm':
		return n
	case 'k':
		return n / 1024
	default:
		// plain bytes
		fullNum, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0
		}
		return fullNum / (1024 * 1024)
	}
}

// parseServerPort extracts -Dserver.port= from JVM arguments
func parseServerPort(cmdline []string) int {
	for _, arg := range cmdline {
		if strings.HasPrefix(arg, "-Dserver.port=") {
			if port, err := strconv.Atoi(strings.TrimPrefix(arg, "-Dserver.port=")); err == nil {
				return port
			}
		}
	}
	return 0
}

// filterPorts resolves the actual Spring application ports without hardcoding.
//
// Strategy:
//  1. If ports are explicitly declared via JVM/Spring args → use only those.
//  2. Otherwise → HTTP-probe each detected port in parallel (200 ms timeout).
//     Ports that respond with a valid HTTP response are real HTTP servers.
//     JMX, debug agents, and other non-HTTP listeners are silently dropped.
//  3. If nothing responds to HTTP → return all detected ports as fallback.
func filterPorts(detected []int, cmdline []string) []int {
	// Step 1: explicitly configured ports take priority
	configured := parseDeclaredPorts(cmdline)
	if len(configured) > 0 {
		configSet := make(map[int]bool, len(configured))
		for _, p := range configured {
			configSet[p] = true
		}
		var result []int
		for _, p := range detected {
			if configSet[p] {
				result = append(result, p)
			}
		}
		if len(result) > 0 {
			return result
		}
		return configured
	}

	// Step 2: no declared ports — probe each one for HTTP
	httpPorts := probeHTTPPorts(detected)
	if len(httpPorts) > 0 {
		return httpPorts
	}

	// Step 3: fallback — nothing filtered
	return detected
}

// probeHTTPPorts sends a GET / to each port in parallel and returns only
// those that reply with a valid HTTP response within 200 ms.
func probeHTTPPorts(ports []int) []int {
	if len(ports) == 0 {
		return nil
	}

	type result struct {
		port int
		ok   bool
	}

	probeClient := &http.Client{Timeout: 200 * time.Millisecond}
	ch := make(chan result, len(ports))

	for _, p := range ports {
		go func(port int) {
			resp, err := probeClient.Get(fmt.Sprintf("http://localhost:%d/", port))
			if err == nil {
				resp.Body.Close()
				ch <- result{port, true}
			} else {
				ch <- result{port, false}
			}
		}(p)
	}

	okSet := make(map[int]bool, len(ports))
	for range ports {
		r := <-ch
		okSet[r.port] = r.ok
	}

	// Preserve original order
	var httpPorts []int
	for _, p := range ports {
		if okSet[p] {
			httpPorts = append(httpPorts, p)
		}
	}
	return httpPorts
}

// parseDeclaredPorts extracts every port number explicitly set via JVM / Spring Boot arguments.
func parseDeclaredPorts(cmdline []string) []int {
	prefixes := []string{
		"-Dserver.port=",
		"--server.port=",
		"-Dmanagement.server.port=",
		"--management.server.port=",
	}
	seen := make(map[int]bool)
	var ports []int
	for _, arg := range cmdline {
		for _, prefix := range prefixes {
			if strings.HasPrefix(arg, prefix) {
				if p, err := strconv.Atoi(strings.TrimPrefix(arg, prefix)); err == nil && !seen[p] {
					seen[p] = true
					ports = append(ports, p)
				}
			}
		}
	}
	return ports
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

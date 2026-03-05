//go:build darwin

package process

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Scan finds running Spring Boot processes on macOS via ps and lsof
func Scan() ([]*SpringProcess, error) {
	out, err := exec.Command("ps", "-eo", "pid,rss,args").Output()
	if err != nil {
		return nil, err
	}

	var processes []*SpringProcess
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Scan() // skip header

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		rssKB, _ := strconv.ParseInt(fields[1], 10, 64)
		cmdline := fields[2:]

		cmdStr := strings.Join(cmdline, " ")
		if !strings.Contains(cmdStr, "java") {
			continue
		}
		if !isSpringCmd(cmdStr) {
			continue
		}

		proc := &SpringProcess{
			PID:              pid,
			CmdLine:          cmdline,
			Name:             parseAppName(cmdline),
			JarFile:          parseJarFile(cmdline),
			LogFile:          parseLogFile(cmdline),
			ActuatorPort:     parseActuatorPort(cmdline),
			ActuatorBasePath: parseActuatorBasePath(cmdline),
			MemoryMB:         rssKB / 1024,
			StartTime:        approximateStartTime(pid),
			Profiles:         parseProfiles(cmdline),
			JavaVersion:      parseJavaVersion(cmdline),
			XmxMB:            parseXmx(cmdline),
			Threads:          getThreadCountDarwin(pid),
		}

		proc.Ports = filterPorts(getPortsDarwin(pid), cmdline)
		if proc.ActuatorPort == 0 && len(proc.Ports) > 0 {
			proc.ActuatorPort = proc.Ports[0]
		}

		processes = append(processes, proc)
	}
	return processes, nil
}

func isSpringCmd(cmdStr string) bool {
	lower := strings.ToLower(cmdStr)
	for _, ind := range []string{"spring", "-dspring", "-jar", "org.springframework"} {
		if strings.Contains(lower, ind) {
			return true
		}
	}
	return false
}

func getThreadCountDarwin(pid int) int {
	out, err := exec.Command("ps", "-o", "thcount=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	t, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return t
}

func approximateStartTime(pid int) time.Time {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return time.Now()
	}
	// lstart format: "Mon Jan  2 15:04:05 2006"
	t, err := time.Parse("Mon Jan  2 15:04:05 2006", strings.TrimSpace(string(out)))
	if err != nil {
		// Try with single-digit day
		t, err = time.Parse("Mon Jan 2 15:04:05 2006", strings.TrimSpace(string(out)))
		if err != nil {
			return time.Now()
		}
	}
	return t
}

func getPortsDarwin(pid int) []int {
	out, err := exec.Command("lsof", "-Pan", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN").Output()
	if err != nil {
		return nil
	}

	seen := make(map[int]bool)
	var ports []int

	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "LISTEN") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		addr := fields[8]
		colonIdx := strings.LastIndex(addr, ":")
		if colonIdx < 0 {
			continue
		}
		port, err := strconv.Atoi(addr[colonIdx+1:])
		if err != nil {
			continue
		}
		if !seen[port] {
			seen[port] = true
			ports = append(ports, port)
		}
	}
	return ports
}

//go:build linux

package process

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Scan finds all running Spring Boot processes on Linux via /proc
func Scan() ([]*SpringProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("reading /proc: %w", err)
	}

	socketInodes := getListeningSocketInodes()

	var processes []*SpringProcess
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		proc, err := scanProcess(pid, socketInodes)
		if err != nil || proc == nil {
			continue
		}
		processes = append(processes, proc)
	}
	return processes, nil
}

func scanProcess(pid int, socketInodes map[uint64]int) (*SpringProcess, error) {
	cmdlineBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, err
	}

	// cmdline is null-byte delimited
	parts := strings.Split(string(cmdlineBytes), "\x00")
	var cmdline []string
	for _, p := range parts {
		if p != "" {
			cmdline = append(cmdline, p)
		}
	}

	if !isSpringProcess(cmdline) {
		return nil, nil
	}

	proc := &SpringProcess{
		PID:              pid,
		CmdLine:          cmdline,
		Name:             parseAppName(cmdline),
		JarFile:          parseJarFile(cmdline),
		LogFile:          parseLogFile(cmdline),
		ActuatorPort:     parseActuatorPort(cmdline),
		ActuatorBasePath: parseActuatorBasePath(cmdline),
		Profiles:         parseProfiles(cmdline),
		JavaVersion:      parseJavaVersion(cmdline),
		XmxMB:            parseXmx(cmdline),
	}

	if wd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
		proc.WorkingDir = wd
	}

	fillProcInfo(proc)

	proc.Ports = filterPorts(getProcessPorts(pid, socketInodes), cmdline)

	if proc.ActuatorPort == 0 && len(proc.Ports) > 0 {
		proc.ActuatorPort = proc.Ports[0]
	}

	return proc, nil
}

func isSpringProcess(cmdline []string) bool {
	if len(cmdline) == 0 {
		return false
	}

	binary := filepath.Base(cmdline[0])
	isJava := binary == "java" || strings.HasPrefix(binary, "java")
	if !isJava && !strings.Contains(cmdline[0], "/java") {
		return false
	}

	cmdStr := strings.Join(cmdline, " ")
	if isKnownNonSpringJava(cmdStr) {
		return false
	}
	lower := strings.ToLower(cmdStr)
	for _, ind := range []string{"spring", "-dspring", "org.springframework", "-jar"} {
		if strings.Contains(lower, ind) {
			return true
		}
	}
	return false
}

func fillProcInfo(proc *SpringProcess) {
	statBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", proc.PID))
	if err != nil {
		return
	}

	statStr := string(statBytes)
	closeParenIdx := strings.LastIndex(statStr, ")")
	if closeParenIdx < 0 {
		return
	}

	// Fields after "pid (name) ": state ppid ... starttime(index 19)
	fields := strings.Fields(statStr[closeParenIdx+2:])
	if len(fields) <= 19 {
		return
	}

	startTicks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return
	}

	var sysinfo syscall.Sysinfo_t
	if err := syscall.Sysinfo(&sysinfo); err != nil {
		return
	}

	const clkTck = int64(100) // typical Linux HZ
	bootTime := time.Now().Unix() - sysinfo.Uptime
	proc.StartTime = time.Unix(bootTime+startTicks/clkTck, 0)

	// Memory from /proc/<pid>/status
	statusBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", proc.PID))
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(strings.NewReader(string(statusBytes)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			if parts := strings.Fields(line); len(parts) >= 2 {
				if kb, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
					proc.MemoryMB = kb / 1024
				}
			}
		}
		if strings.HasPrefix(line, "Threads:") {
			if parts := strings.Fields(line); len(parts) >= 2 {
				if t, err := strconv.Atoi(parts[1]); err == nil {
					proc.Threads = t
				}
			}
		}
	}
}

// getListeningSocketInodes returns map[inode]port for all LISTEN TCP sockets
func getListeningSocketInodes() map[uint64]int {
	result := make(map[uint64]int)
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Scan() // skip header
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 10 {
				continue
			}
			// State 0A = TCP_LISTEN
			if fields[3] != "0A" {
				continue
			}
			localAddr := fields[1]
			colonIdx := strings.LastIndex(localAddr, ":")
			if colonIdx < 0 {
				continue
			}
			port, err := strconv.ParseInt(localAddr[colonIdx+1:], 16, 32)
			if err != nil {
				continue
			}
			inode, err := strconv.ParseUint(fields[9], 10, 64)
			if err != nil {
				continue
			}
			result[inode] = int(port)
		}
		f.Close()
	}
	return result
}

// getProcessPorts matches /proc/<pid>/fd socket symlinks to listening inodes
func getProcessPorts(pid int, socketInodes map[uint64]int) []int {
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return nil
	}

	seen := make(map[int]bool)
	var ports []int

	for _, entry := range entries {
		link, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
		if err != nil {
			continue
		}
		// Socket symlinks: "socket:[<inode>]"
		if !strings.HasPrefix(link, "socket:[") || !strings.HasSuffix(link, "]") {
			continue
		}
		inodeStr := link[len("socket:[") : len(link)-1]
		inode, err := strconv.ParseUint(inodeStr, 10, 64)
		if err != nil {
			continue
		}
		if port, ok := socketInodes[inode]; ok && !seen[port] {
			seen[port] = true
			ports = append(ports, port)
		}
	}
	return ports
}

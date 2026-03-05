package ui

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mrkayhyun/spring-monitor/internal/actuator"
	"github.com/mrkayhyun/spring-monitor/internal/process"
)

type appState int

const (
	stateList appState = iota
	stateLog
	stateDescribe
	stateKill
)

// App is the main TUI application
type App struct {
	term      *Terminal
	version   string
	mu        sync.Mutex

	state     appState
	processes []*process.SpringProcess
	selected  int

	logViewer  *LogViewer
	killTarget *process.SpringProcess
	killInfo   *actuator.Info

	statusMsg string
	statusErr bool
}

func NewApp(version string) *App {
	return &App{
		term:    NewTerminal(),
		version: version,
	}
}

func (a *App) Init() error {
	if err := a.term.MakeRaw(); err != nil {
		return err
	}
	HideCursor()
	Clear()
	return nil
}

func (a *App) Cleanup() {
	ShowCursor()
	Clear()
	a.term.Restore()
}

func (a *App) setProcesses(procs []*process.SpringProcess) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.processes = procs
	if a.selected >= len(procs) {
		a.selected = max(0, len(procs)-1)
	}
}

func (a *App) setStatus(msg string, isErr bool) {
	a.statusMsg = msg
	a.statusErr = isErr
}

// Run starts the main event loop
func (a *App) Run(initialProcs []*process.SpringProcess) {
	a.setProcesses(initialProcs)

	keyCh := make(chan int, 8)
	procRefreshCh := make(chan []*process.SpringProcess, 1)

	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)

	// Background key reader
	go func() {
		for {
			keyCh <- ReadKey()
		}
	}()

	// Background process refresh every 5 seconds
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if procs, err := process.Scan(); err == nil {
				select {
				case procRefreshCh <- procs:
				default:
				}
			}
		}
	}()

	a.render()

	for {
		// Determine log update channel (nil = disabled)
		var logUpdateCh <-chan struct{}
		if a.state == stateLog && a.logViewer != nil {
			logUpdateCh = a.logViewer.UpdateCh
		}

		select {
		case key := <-keyCh:
			if !a.handleKey(key) {
				return
			}
			a.render()

		case procs := <-procRefreshCh:
			if a.state == stateList {
				a.setProcesses(procs)
				a.render()
			}

		case <-logUpdateCh:
			if a.state == stateLog && a.logViewer != nil {
				if a.logViewer.IsFollow() {
					a.logViewer.ScrollToBottom(a.term.Height - 3)
				}
				a.render()
			}

		case <-sigwinch:
			a.term.Refresh()
			a.render()
		}
	}
}

func (a *App) handleKey(key int) bool {
	switch a.state {
	case stateList:
		return a.handleListKey(key)
	case stateLog:
		return a.handleLogKey(key)
	case stateDescribe:
		return a.handleDescribeKey(key)
	case stateKill:
		return a.handleKillKey(key)
	}
	return true
}

func (a *App) handleListKey(key int) bool {
	procs := a.processes
	switch key {
	case 'q', KeyCtrlC, KeyCtrlD:
		return false

	case KeyUp, 'k':
		if a.selected > 0 {
			a.selected--
		}
	case KeyDown, 'j':
		if a.selected < len(procs)-1 {
			a.selected++
		}

	case 'l', KeyEnter:
		if len(procs) > 0 {
			a.openLog(procs[a.selected])
		}

	case 'd':
		if len(procs) > 0 {
			a.state = stateDescribe
			// Probe actuator in background
			go func(proc *process.SpringProcess) {
				url := proc.ActuatorURL()
				if url == "" {
					proc.ActuatorStatus = process.ActuatorDisabled
					return
				}
				info, _ := actuator.Check(url)
				if info != nil && info.Available {
					proc.ActuatorStatus = process.ActuatorEnabled
				} else {
					proc.ActuatorStatus = process.ActuatorDisabled
				}
				a.render()
			}(procs[a.selected])
		}

	case 'K':
		if len(procs) > 0 {
			a.openKill(procs[a.selected])
		}

	case 'r':
		procs, err := process.Scan()
		if err == nil {
			a.setProcesses(procs)
			a.setStatus("Refreshed", false)
		} else {
			a.setStatus(fmt.Sprintf("Refresh failed: %v", err), true)
		}
	}
	return true
}

func (a *App) handleLogKey(key int) bool {
	lv := a.logViewer
	switch key {
	case 'q', KeyEsc:
		if lv != nil {
			lv.Stop()
			a.logViewer = nil
		}
		a.state = stateList

	case 'f':
		if lv != nil {
			lv.ToggleFollow(a.term.Height - 3)
		}

	case KeyUp, 'k':
		if lv != nil {
			lv.ScrollUp(3)
		}
	case KeyDown, 'j':
		if lv != nil {
			lv.ScrollDown(3)
		}

	case KeyPgUp:
		if lv != nil {
			lv.ScrollUp(a.term.Height - 4)
		}
	case KeyPgDn:
		if lv != nil {
			lv.ScrollDown(a.term.Height - 4)
		}
	case 'g':
		if lv != nil {
			lv.offset = 0
		}
	case 'G':
		if lv != nil {
			lv.ScrollToBottom(a.term.Height - 3)
		}
	}
	return true
}

func (a *App) handleDescribeKey(key int) bool {
	switch key {
	case 'q', KeyEsc, 'b':
		a.state = stateList
	}
	return true
}

func (a *App) handleKillKey(key int) bool {
	proc := a.killTarget
	switch key {
	case KeyEsc, 'c':
		a.state = stateList
		a.killTarget = nil
		a.killInfo = nil

	case 'g': // graceful shutdown via actuator
		if proc != nil && a.killInfo != nil && a.killInfo.ShutdownAvailable {
			err := actuator.GracefulShutdown(proc.ActuatorURL())
			if err != nil {
				a.setStatus(fmt.Sprintf("Actuator shutdown failed: %v", err), true)
			} else {
				a.setStatus(fmt.Sprintf("[%s] Graceful shutdown sent via actuator", proc.Name), false)
			}
			a.state = stateList
			a.killTarget = nil
			a.killInfo = nil
		}

	case 't': // SIGTERM
		if proc != nil {
			if p, err := os.FindProcess(proc.PID); err == nil {
				p.Signal(syscall.SIGTERM)
				a.setStatus(fmt.Sprintf("[%s] SIGTERM sent (PID %d)", proc.Name, proc.PID), false)
			} else {
				a.setStatus(fmt.Sprintf("Process not found: %v", err), true)
			}
			a.state = stateList
			a.killTarget = nil
			a.killInfo = nil
		}

	case 'K': // SIGKILL
		if proc != nil {
			if p, err := os.FindProcess(proc.PID); err == nil {
				p.Signal(syscall.SIGKILL)
				a.setStatus(fmt.Sprintf("[%s] SIGKILL sent (PID %d)", proc.Name, proc.PID), false)
			} else {
				a.setStatus(fmt.Sprintf("Process not found: %v", err), true)
			}
			a.state = stateList
			a.killTarget = nil
			a.killInfo = nil
		}
	}
	return true
}

func (a *App) openLog(proc *process.SpringProcess) {
	logFile := proc.FindLogFile()
	if logFile == "" {
		a.setStatus(fmt.Sprintf("No log file found for %s", proc.Name), true)
		return
	}
	lv, err := NewLogViewer(logFile, proc.Name, proc.PID)
	if err != nil {
		a.setStatus(fmt.Sprintf("Cannot open log: %v", err), true)
		return
	}
	// Start at bottom
	lv.ScrollToBottom(a.term.Height - 3)
	a.logViewer = lv
	a.state = stateLog
}

func (a *App) openKill(proc *process.SpringProcess) {
	a.killTarget = proc
	a.killInfo = nil
	a.state = stateKill

	// Probe actuator in background
	go func() {
		url := proc.ActuatorURL()
		if url == "" {
			return
		}
		info, _ := actuator.Check(url)
		a.mu.Lock()
		a.killInfo = info
		a.mu.Unlock()
		a.render()
	}()
}

// ─── Rendering ───────────────────────────────────────────────────────────────

func (a *App) render() {
	switch a.state {
	case stateList:
		a.renderList()
	case stateLog:
		if a.logViewer != nil {
			a.logViewer.Render(a.term, a.logViewer.ProcName, a.logViewer.PID)
		}
	case stateDescribe:
		a.renderDescribe()
	case stateKill:
		a.renderKill()
	}
}

func (a *App) renderList() {
	w := a.term.Width
	h := a.term.Height
	Clear()

	// ── Row 1: Header ──────────────────────────────────────────────────────
	MoveTo(1, 1)
	now := time.Now().Format("15:04:05")
	left := fmt.Sprintf(" spring-monitor %s  │  q:quit  l:logs  K:kill  d:describe  r:refresh  ↑↓jk:nav", a.version)
	right := fmt.Sprintf(" %s ", now)
	gap := w - visibleLen(left) - visibleLen(right)
	if gap < 0 {
		gap = 0
	}
	header := BgBlue + Bold + White + left + strings.Repeat(" ", gap) + right + Reset
	fmt.Print(header)

	// ── Row 2: Column headers ───────────────────────────────────────────────
	MoveTo(2, 1)
	colHeader := fmt.Sprintf("  %-24s %-7s %-14s %-10s %-8s  %s",
		"NAME", "PID", "PORT(S)", "UPTIME", "MEM(MB)", "ACTUATOR")
	fmt.Print(BgGray + Bold + padRight(colHeader, w) + Reset)

	// ── Rows 3..h-1: Process rows ───────────────────────────────────────────
	procs := a.processes
	contentRows := h - 3
	startIdx := 0
	if a.selected >= contentRows {
		startIdx = a.selected - contentRows + 1
	}

	for i := 0; i < contentRows; i++ {
		MoveTo(i+3, 1)
		idx := startIdx + i
		if idx < len(procs) {
			a.renderProcessRow(procs[idx], idx == a.selected, w)
		} else {
			fmt.Print(strings.Repeat(" ", w))
		}
	}

	// ── Row h: Status bar ───────────────────────────────────────────────────
	MoveTo(h, 1)
	status := fmt.Sprintf(" %d Spring app(s) running", len(procs))
	if a.statusMsg != "" {
		if a.statusErr {
			status += "  │  " + Red + a.statusMsg + Reset
		} else {
			status += "  │  " + Green + a.statusMsg + Reset
		}
	}
	fmt.Print(BgBlack + White + padRight(status, w) + Reset)
}

func (a *App) renderProcessRow(proc *process.SpringProcess, selected bool, w int) {
	// Build actuator indicator (plain text, no ANSI inside padded region)
	actuatorStr := actuatorStr(proc)

	// Plain-text fields
	name := truncate(proc.Name, 24)
	pid := fmt.Sprintf("%d", proc.PID)
	ports := truncate(proc.PortsString(), 14)
	uptime := proc.Uptime()
	mem := fmt.Sprintf("%d", proc.MemoryMB)

	cursor := "  "
	if selected {
		cursor = " ▶"
	}

	// Build row (actuator at the end, may have ANSI)
	plain := fmt.Sprintf("%s %-24s %-7s %-14s %-10s %-8s  ",
		cursor, name, pid, ports, uptime, mem)
	full := plain + actuatorStr

	if selected {
		fmt.Print(BgCyan + Bold + padRight(full, w) + Reset)
	} else {
		fmt.Print(padRight(full, w))
	}
}

func actuatorStr(proc *process.SpringProcess) string {
	switch proc.ActuatorStatus {
	case process.ActuatorEnabled:
		return Green + fmt.Sprintf("✓  :%d", proc.ActuatorPort) + Reset
	case process.ActuatorDisabled:
		return Red + "✗" + Reset
	default:
		if proc.ActuatorPort > 0 {
			return Yellow + fmt.Sprintf("?  :%d", proc.ActuatorPort) + Reset
		}
		return Dim + "-" + Reset
	}
}

func (a *App) renderDescribe() {
	w := a.term.Width
	h := a.term.Height
	Clear()

	procs := a.processes
	if a.selected >= len(procs) {
		a.state = stateList
		a.render()
		return
	}
	proc := procs[a.selected]

	// Header
	MoveTo(1, 1)
	title := fmt.Sprintf(" Describe: %s (PID: %d)", proc.Name, proc.PID)
	keys := "  q/ESC:back"
	fmt.Print(BgBlue + Bold + White + padRight(title+keys, w) + Reset)

	row := 3
	printField := func(label, value string) {
		MoveTo(row, 1)
		fmt.Print(padRight(fmt.Sprintf("  %-20s %s", label, value), w))
		row++
	}
	printSection := func(title string) {
		row++
		MoveTo(row, 1)
		fmt.Print(Bold + Cyan + padRight(" "+title, w) + Reset)
		row++
	}

	printSection("Process")
	printField("Name:", proc.Name)
	printField("PID:", fmt.Sprintf("%d", proc.PID))
	if proc.JarFile != "" {
		printField("JAR:", truncate(proc.JarFile, w-24))
	}
	if proc.WorkingDir != "" {
		printField("Working Dir:", truncate(proc.WorkingDir, w-24))
	}
	printField("Port(s):", proc.PortsString())
	printField("Uptime:", proc.Uptime())
	printField("Memory:", fmt.Sprintf("%d MB (RSS)", proc.MemoryMB))
	if lf := proc.FindLogFile(); lf != "" {
		printField("Log File:", truncate(lf, w-24))
	} else {
		printField("Log File:", Dim+"not found"+Reset)
	}

	printSection("Actuator")
	url := proc.ActuatorURL()
	if url == "" {
		printField("Status:", Dim+"no management port detected"+Reset)
	} else {
		printField("URL:", url)
		switch proc.ActuatorStatus {
		case process.ActuatorEnabled:
			printField("Status:", Green+"✓ Enabled"+Reset)
		case process.ActuatorDisabled:
			printField("Status:", Red+"✗ Not available"+Reset)
		default:
			printField("Status:", Yellow+"? Probing... (press d again)"+Reset)
		}
	}

	printSection("Command Line")
	// Show command line (wrapped)
	cmdStr := strings.Join(proc.CmdLine, " ")
	maxCmdW := w - 4
	for len(cmdStr) > 0 && row < h-1 {
		chunk := cmdStr
		if len(chunk) > maxCmdW {
			chunk = chunk[:maxCmdW]
		}
		MoveTo(row, 1)
		fmt.Print("  " + Dim + chunk + Reset + strings.Repeat(" ", max(0, w-len(chunk)-2)))
		cmdStr = cmdStr[len(chunk):]
		row++
	}

	// Status bar
	MoveTo(h, 1)
	fmt.Print(BgBlack + White + padRight(" q/ESC to go back", w) + Reset)
}

func (a *App) renderKill() {
	w := a.term.Width
	h := a.term.Height
	Clear()

	proc := a.killTarget
	if proc == nil {
		a.state = stateList
		a.render()
		return
	}

	// Header
	MoveTo(1, 1)
	fmt.Print(BgBlue + Bold + White + padRight(fmt.Sprintf(" Kill Process: %s (PID: %d)", proc.Name, proc.PID), w) + Reset)

	row := 3
	print := func(s string) {
		MoveTo(row, 1)
		fmt.Print(padRight("  "+s, w))
		row++
	}

	print(fmt.Sprintf("Process: %s%s%s  PID: %d  Port(s): %s  Uptime: %s",
		Bold, proc.Name, Reset, proc.PID, proc.PortsString(), proc.Uptime()))
	row++

	// Actuator status
	a.mu.Lock()
	info := a.killInfo
	a.mu.Unlock()

	if proc.ActuatorURL() != "" {
		if info == nil {
			print(Yellow + "Actuator: probing " + proc.ActuatorURL() + " ..." + Reset)
		} else if info.Available {
			shutdownStr := ""
			if info.ShutdownAvailable {
				shutdownStr = Green + " (shutdown endpoint available)" + Reset
			} else {
				shutdownStr = Yellow + " (shutdown endpoint NOT enabled)" + Reset
			}
			print(Green + "Actuator: ✓ " + proc.ActuatorURL() + shutdownStr)
		} else {
			print(Red + "Actuator: ✗ not reachable at " + proc.ActuatorURL() + Reset)
		}
	} else {
		print(Dim + "Actuator: not detected" + Reset)
	}
	row++

	print("─────────────────────────────────────────")
	row++

	// Action options
	if info != nil && info.Available && info.ShutdownAvailable {
		print(Green + "[g]" + Reset + " Graceful shutdown  (POST /actuator/shutdown)")
	} else {
		print(Dim + "[g]  Graceful shutdown  (actuator/shutdown not available)" + Reset)
	}
	print(Yellow + "[t]" + Reset + " SIGTERM            (request graceful terminate)")
	print(Red + "[K]" + Reset + " SIGKILL            (force kill - no cleanup)")
	row++
	print(Dim + "[ESC/c] Cancel" + Reset)

	// Status bar
	MoveTo(h, 1)
	fmt.Print(BgBlack + White + padRight(" Choose an action or ESC to cancel", w) + Reset)
}

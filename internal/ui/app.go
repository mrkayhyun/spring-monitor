package ui

import (
	"fmt"
	"os"
	"os/signal"
	"sort"
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

type sortField int

const (
	sortByName sortField = iota
	sortByMemory
	sortByUptime
	sortByPort
)

// App is the main TUI application
type App struct {
	term     *Terminal
	version  string
	mu       sync.Mutex
	redrawCh chan struct{}

	state     appState
	processes []*process.SpringProcess
	selected  int

	logViewer *LogViewer

	describeTarget     *process.SpringProcess
	describeMetrics    *actuator.Metrics
	describeInfo       *actuator.AppInfo
	describeLoading    bool
	describeGeneration uint64

	killTarget *process.SpringProcess
	killInfo   *actuator.Info

	// log search input mode
	searchMode  bool
	searchInput string

	// sort state
	sortBy   sortField
	sortDesc bool

	// last refresh time
	lastRefresh time.Time

	statusMsg string
	statusErr bool
}

func NewApp(version string) *App {
	return &App{
		term:     NewTerminal(),
		version:  version,
		redrawCh: make(chan struct{}, 1),
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
	selectedPID := a.selectedPIDLocked()
	a.sortProcesses(procs)
	a.processes = procs
	a.restoreSelectionLocked(selectedPID)
	a.lastRefresh = time.Now()
}

func (a *App) selectedPIDLocked() int {
	if a.selected >= 0 && a.selected < len(a.processes) {
		return a.processes[a.selected].PID
	}
	return 0
}

func (a *App) restoreSelectionLocked(pid int) {
	if pid != 0 {
		for i, proc := range a.processes {
			if proc.PID == pid {
				a.selected = i
				return
			}
		}
	}
	if a.selected >= len(a.processes) {
		a.selected = max(0, len(a.processes)-1)
	}
}

// sortProcesses sorts procs in-place according to a.sortBy / a.sortDesc.
// Must be called with a.mu held.
func (a *App) sortProcesses(procs []*process.SpringProcess) {
	sort.SliceStable(procs, func(i, j int) bool {
		left, right := procs[i], procs[j]
		cmp := 0
		switch a.sortBy {
		case sortByMemory:
			cmp = compareInt64(left.MemoryMB, right.MemoryMB)
		case sortByUptime:
			// A newer start time means a shorter uptime.
			cmp = -left.StartTime.Compare(right.StartTime)
		case sortByPort:
			cmp = compareInt(firstPort(left), firstPort(right))
		default: // sortByName
			cmp = strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
		}
		if cmp == 0 {
			cmp = strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
		}
		if cmp == 0 {
			cmp = compareInt(left.PID, right.PID)
		}
		if a.sortDesc {
			return cmp > 0
		}
		return cmp < 0
	})
}

func firstPort(proc *process.SpringProcess) int {
	if len(proc.Ports) == 0 {
		return 0
	}
	return proc.Ports[0]
}

func compareInt(left, right int) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compareInt64(left, right int64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func (a *App) setStatus(msg string, isErr bool) {
	a.statusMsg = msg
	a.statusErr = isErr
}

func (a *App) requestRender() {
	select {
	case a.redrawCh <- struct{}{}:
	default:
	}
}

// Run starts the main event loop
func (a *App) Run(initialProcs []*process.SpringProcess) {
	a.lastRefresh = time.Now()
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

	// Background process refresh every 5 seconds (with actuator health checks)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			procs, err := process.Scan()
			if err != nil {
				continue
			}
			enrichWithActuator(procs)
			select {
			case procRefreshCh <- procs:
			default:
			}
		}
	}()

	// Enrich copies of the initial processes so the visible slice is never
	// mutated concurrently with rendering.
	go func() {
		enriched := make([]*process.SpringProcess, len(initialProcs))
		for i, proc := range initialProcs {
			clone := *proc
			enriched[i] = &clone
		}
		enrichWithActuator(enriched)
		select {
		case procRefreshCh <- enriched:
		default:
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

		case <-a.redrawCh:
			a.render()

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
			a.openDescribe(procs[a.selected])
		}

	case 'K':
		if len(procs) > 0 {
			a.openKill(procs[a.selected])
		}

	case 's':
		a.mu.Lock()
		selectedPID := a.selectedPIDLocked()
		if a.sortDesc {
			// cycle: asc → desc → next field asc
			a.sortBy = (a.sortBy + 1) % 4
			a.sortDesc = false
		} else {
			a.sortDesc = true
		}
		a.sortProcesses(a.processes)
		a.restoreSelectionLocked(selectedPID)
		a.mu.Unlock()

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

	// Search input mode: collect characters until Enter or ESC
	if a.searchMode {
		switch key {
		case KeyEnter:
			a.searchMode = false
			if lv != nil {
				lv.SetSearch(a.searchInput)
				if idx := lv.SearchNext(); idx >= 0 {
					lv.mu.Lock()
					displayH := a.term.Height - 3
					if idx < lv.offset || idx >= lv.offset+displayH {
						lv.offset = max(0, idx-displayH/2)
					}
					lv.mu.Unlock()
				}
			}
		case KeyEsc:
			a.searchMode = false
			a.searchInput = ""
			if lv != nil {
				lv.ClearSearch()
			}
		case 127, 8: // Backspace
			if len(a.searchInput) > 0 {
				a.searchInput = a.searchInput[:len(a.searchInput)-1]
			}
		default:
			if key >= 32 && key < 127 {
				a.searchInput += string(rune(key))
			}
		}
		// Re-render search bar (show input as it's typed)
		a.renderSearchBar()
		return true
	}

	switch key {
	case 'q', KeyEsc:
		if lv != nil {
			lv.Stop()
			a.logViewer = nil
		}
		a.searchMode = false
		a.searchInput = ""
		a.state = stateList

	case '/':
		a.searchMode = true
		a.searchInput = ""

	case 'n':
		if lv != nil && lv.IsSearchActive() {
			if idx := lv.SearchNext(); idx >= 0 {
				lv.mu.Lock()
				displayH := a.term.Height - 3
				if idx < lv.offset || idx >= lv.offset+displayH {
					lv.offset = max(0, idx-displayH/2)
				}
				lv.mu.Unlock()
			}
		}
	case 'N':
		if lv != nil && lv.IsSearchActive() {
			if idx := lv.SearchPrev(); idx >= 0 {
				lv.mu.Lock()
				displayH := a.term.Height - 3
				if idx < lv.offset || idx >= lv.offset+displayH {
					lv.offset = max(0, idx-displayH/2)
				}
				lv.mu.Unlock()
			}
		}

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
			lv.ScrollToTop()
		}
	case 'G':
		if lv != nil {
			lv.ScrollToBottom(a.term.Height - 3)
		}
	}
	return true
}

// renderSearchBar redraws only the second row and status bar during search input,
// to give live feedback without a full re-render.
func (a *App) renderSearchBar() {
	w := a.term.Width
	h := a.term.Height
	MoveTo(2, 1)
	fmt.Print(BgGray + Dim + padRight(fmt.Sprintf(" Search: %s%s%s_", Bold, a.searchInput, Reset+BgGray+Dim), w) + Reset)
	MoveTo(h, 1)
	fmt.Print(BgBlack + White + padRight(" Type search query, Enter to confirm, ESC to cancel", w) + Reset)
}

func (a *App) handleDescribeKey(key int) bool {
	switch key {
	case 'q', KeyEsc, 'b':
		a.mu.Lock()
		a.state = stateList
		a.describeTarget = nil
		a.describeMetrics = nil
		a.describeInfo = nil
		a.describeLoading = false
		a.describeGeneration++
		a.mu.Unlock()
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

func (a *App) openDescribe(proc *process.SpringProcess) {
	a.mu.Lock()
	a.state = stateDescribe
	a.describeTarget = proc
	a.describeMetrics = nil
	a.describeInfo = nil
	a.describeLoading = proc.ActuatorURL() != ""
	a.describeGeneration++
	generation := a.describeGeneration
	a.mu.Unlock()

	url := proc.ActuatorURL()
	if url == "" {
		proc.ActuatorStatus = process.ActuatorDisabled
		return
	}

	go func() {
		actuatorInfo, _ := actuator.Check(url)
		var metrics *actuator.Metrics
		var appInfo *actuator.AppInfo
		if actuatorInfo != nil && actuatorInfo.Available {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				metrics, _ = actuator.GetMetrics(url)
			}()
			go func() {
				defer wg.Done()
				appInfo, _ = actuator.GetInfo(url)
			}()
			wg.Wait()
		}

		a.mu.Lock()
		if a.describeTarget != proc || a.describeGeneration != generation {
			a.mu.Unlock()
			return
		}
		if actuatorInfo != nil && actuatorInfo.Available {
			proc.ActuatorStatus = process.ActuatorEnabled
			proc.HealthStatus = actuatorInfo.Health
		} else {
			proc.ActuatorStatus = process.ActuatorDisabled
		}
		a.describeMetrics = metrics
		a.describeInfo = appInfo
		a.describeLoading = false
		a.mu.Unlock()
		a.requestRender()
	}()
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
		if a.killTarget != proc {
			a.mu.Unlock()
			return
		}
		a.killInfo = info
		a.mu.Unlock()
		a.requestRender()
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
			if a.searchMode {
				a.renderSearchBar()
			}
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
	right := fmt.Sprintf(" %s ", now)
	left := fmt.Sprintf(" spring-monitor %s  │  q:quit  l:logs  K:kill  d:describe  s:sort  r:refresh  ↑↓jk:nav", a.version)
	header := padRight(left, w-visibleLen(right)) + right
	fmt.Print(BgBlue + Bold + White + header + Reset)

	// ── Row 2: Column headers (highlight active sort column) ────────────────
	MoveTo(2, 1)
	sortArrow := func(f sortField, label string) string {
		if a.sortBy == f {
			arrow := "↑"
			if a.sortDesc {
				arrow = "↓"
			}
			return Bold + label + arrow + Reset + BgGray
		}
		return label
	}
	colHeader := fmt.Sprintf("  %-20s %-7s %-10s %-8s %-7s %-5s %-10s %-8s  %s",
		sortArrow(sortByName, "NAME"), "PID",
		sortArrow(sortByPort, "PORT(S)"),
		sortArrow(sortByUptime, "UPTIME"),
		sortArrow(sortByMemory, "MEM(MB)"),
		"JAVA", "PROFILE", "HEALTH", "ACTUATOR")
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
	plain := fmt.Sprintf(" %d Spring app(s) running", len(procs))
	if a.statusMsg != "" {
		plain += "  │  " + a.statusMsg
	}
	if !a.lastRefresh.IsZero() {
		elapsed := time.Since(a.lastRefresh).Round(time.Second)
		plain += fmt.Sprintf("  │  refreshed %s ago", elapsed)
	}
	fmt.Print(BgBlack + White + padRight(plain, w) + Reset)
}

func (a *App) renderProcessRow(proc *process.SpringProcess, selected bool, w int) {
	cursor := "  "
	if selected {
		cursor = " ▶"
	}

	name := truncate(proc.Name, 20)
	pid := fmt.Sprintf("%d", proc.PID)
	ports := truncate(proc.PortsString(), 10)
	uptime := truncate(proc.Uptime(), 8)
	mem := fmt.Sprintf("%d", proc.MemoryMB)
	java := proc.JavaVersion
	if java == "" {
		java = "-"
	}
	profile := truncate(proc.Profiles, 10)
	if profile == "" {
		profile = "-"
	}

	// Fixed-width plain section (no ANSI)
	plain := fmt.Sprintf("%s %-20s %-7s %-10s %-8s %-7s %-5s %-10s %-8s  %-14s",
		cursor, name, pid, ports, uptime, mem, java, profile,
		stripAnsi(healthStr(proc.HealthStatus)),
		stripAnsi(actuatorStr(proc)))

	if selected {
		// Selected row: whole row gets BgCyan — use plain text only to avoid
		// inner Reset codes cancelling the row background colour
		fmt.Print(BgCyan + Bold + padRight(plain, w) + Reset)
	} else {
		// Normal row: colour health and actuator individually at the end
		colored := fmt.Sprintf("%s %-20s %-7s %-10s %-8s %-7s %-5s %-10s %-8s  %s",
			cursor, name, pid, ports, uptime, mem, java, profile,
			healthStr(proc.HealthStatus), actuatorStr(proc))
		fmt.Print(padRight(colored, w))
	}
}

func healthStr(status string) string {
	switch status {
	case "UP":
		return Green + "UP" + Reset
	case "DOWN":
		return Red + "DOWN" + Reset
	case "OUT_OF_SERVICE":
		return Yellow + "OOS" + Reset
	case "UNKNOWN":
		return Yellow + "UNK" + Reset
	default:
		return Dim + "-" + Reset
	}
}

func actuatorStr(proc *process.SpringProcess) string {
	switch proc.ActuatorStatus {
	case process.ActuatorEnabled:
		return Green + fmt.Sprintf("✓ :%d", proc.ActuatorPort) + Reset
	case process.ActuatorDisabled:
		return Red + "✗" + Reset
	default:
		if proc.ActuatorPort > 0 {
			return Yellow + fmt.Sprintf("? :%d", proc.ActuatorPort) + Reset
		}
		return Dim + "-" + Reset
	}
}

func (a *App) renderDescribe() {
	w := a.term.Width
	h := a.term.Height
	Clear()

	a.mu.Lock()
	var proc *process.SpringProcess
	if a.describeTarget != nil {
		snapshot := *a.describeTarget
		proc = &snapshot
	}
	metrics := a.describeMetrics
	appInfo := a.describeInfo
	loading := a.describeLoading
	a.mu.Unlock()
	if proc == nil {
		a.state = stateList
		a.render()
		return
	}

	// Header
	MoveTo(1, 1)
	title := fmt.Sprintf(" Describe: %s (PID: %d)", proc.Name, proc.PID)
	keys := "  q/ESC:back"
	fmt.Print(BgBlue + Bold + White + padRight(title+keys, w) + Reset)

	row := 3
	printField := func(label, value string) {
		if row >= h {
			return
		}
		MoveTo(row, 1)
		fmt.Print(padRight(fmt.Sprintf("  %-20s %s", label, value), w))
		row++
	}
	printSection := func(title string) {
		row++
		if row >= h {
			return
		}
		MoveTo(row, 1)
		fmt.Print(Bold + Cyan + padRight(" "+title, w) + Reset)
		row++
	}

	printSection("Process")
	printField("Name:", Bold+proc.Name+Reset)
	printField("PID:", fmt.Sprintf("%d", proc.PID))
	if proc.JavaVersion != "" {
		printField("Java:", "Java "+proc.JavaVersion)
	}
	if proc.Profiles != "" {
		printField("Profiles:", Cyan+proc.Profiles+Reset)
	} else {
		printField("Profiles:", Dim+"(none / default)"+Reset)
	}
	printField("Port(s):", proc.PortsString())
	printField("Uptime:", proc.Uptime())
	printField("Started:", proc.StartTime.Format("2006-01-02 15:04:05"))

	memStr := fmt.Sprintf("%d MB (RSS)", proc.MemoryMB)
	if proc.XmxMB > 0 {
		memStr += fmt.Sprintf("  │  Xmx: %d MB", proc.XmxMB)
	}
	printField("Memory:", memStr)

	if proc.Threads > 0 {
		printField("Threads:", fmt.Sprintf("%d", proc.Threads))
	}
	if proc.JarFile != "" {
		printField("JAR:", truncate(proc.JarFile, w-24))
	}
	if proc.WorkingDir != "" {
		printField("Working Dir:", truncate(proc.WorkingDir, w-24))
	}
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
			if proc.HealthStatus == "" {
				printField("Health:", Dim+"not exposed"+Reset)
			} else {
				printField("Health:", healthStr(proc.HealthStatus))
			}
		case process.ActuatorDisabled:
			printField("Status:", Red+"✗ Not available"+Reset)
		default:
			printField("Status:", Yellow+"? Probing..."+Reset)
		}

		if loading {
			printField("Metrics:", Yellow+"loading..."+Reset)
		} else if metrics != nil && metrics.Available {
			if metrics.HeapUsedAvailable || metrics.HeapMaxAvailable {
				heap := ""
				if metrics.HeapUsedAvailable {
					heap = fmt.Sprintf("%.1f MB used", metrics.HeapUsedMB)
				}
				if metrics.HeapMaxAvailable {
					if heap != "" {
						heap += fmt.Sprintf(" / %.1f MB", metrics.HeapMaxMB)
						if metrics.HeapMaxMB > 0 {
							heap += fmt.Sprintf(" (%.1f%%)", metrics.HeapUsedMB/metrics.HeapMaxMB*100)
						}
					} else {
						heap = fmt.Sprintf("%.1f MB max", metrics.HeapMaxMB)
					}
				}
				printField("JVM Heap:", heap)
			}
			if metrics.NonHeapAvailable {
				printField("JVM Non-heap:", fmt.Sprintf("%.1f MB", metrics.NonHeapMB))
			}
			if metrics.ThreadsAvailable {
				printField("JVM Threads:", fmt.Sprintf("%d live", metrics.ThreadCount))
			}
			if metrics.HTTPAvailable {
				httpParts := []string{fmt.Sprintf("%d total", metrics.HTTPRequestCount)}
				if metrics.HTTPErrorAvailable {
					httpParts = append(httpParts, fmt.Sprintf("%d server errors", metrics.HTTPErrorCount))
				}
				httpParts = append(httpParts, fmt.Sprintf("%.1f ms avg", metrics.HTTPAvgDurationMs))
				printField("HTTP Requests:", strings.Join(httpParts, "  │  "))
			}
		} else if proc.ActuatorStatus == process.ActuatorEnabled {
			printField("Metrics:", Dim+"not exposed"+Reset)
		}
	}

	if !loading && appInfo != nil && (appInfo.AppVersion != "" || appInfo.BuildTime != "" ||
		appInfo.GitBranch != "" || appInfo.GitCommit != "" || len(appInfo.Extra) > 0) {
		printSection("Application")
		if appInfo.AppVersion != "" {
			printField("Version:", appInfo.AppVersion)
		}
		if appInfo.BuildTime != "" {
			printField("Build Time:", appInfo.BuildTime)
		}
		if appInfo.GitBranch != "" {
			printField("Git Branch:", appInfo.GitBranch)
		}
		if appInfo.GitCommit != "" {
			printField("Git Commit:", appInfo.GitCommit)
		}
		keys := make([]string, 0, len(appInfo.Extra))
		for key := range appInfo.Extra {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if row >= h-1 {
				break
			}
			printField(key+":", appInfo.Extra[key])
		}
	}

	printSection("Command Line")
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

// enrichWithActuator probes Spring Actuator for each process in parallel
// and updates ActuatorStatus and HealthStatus fields.
func enrichWithActuator(procs []*process.SpringProcess) {
	var wg sync.WaitGroup
	for _, proc := range procs {
		url := proc.ActuatorURL()
		if url == "" {
			proc.ActuatorStatus = process.ActuatorDisabled
			continue
		}
		wg.Add(1)
		go func(p *process.SpringProcess, u string) {
			defer wg.Done()
			info, _ := actuator.Check(u)
			if info == nil {
				return
			}
			if info.Available {
				p.ActuatorStatus = process.ActuatorEnabled
				p.HealthStatus = info.Health
			} else {
				p.ActuatorStatus = process.ActuatorDisabled
			}
		}(proc, url)
	}
	wg.Wait()
}

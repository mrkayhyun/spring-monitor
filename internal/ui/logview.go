package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// LogViewer handles displaying and following a log file
type LogViewer struct {
	FilePath string
	ProcName string
	PID      int

	mu       sync.Mutex
	lines    []string
	offset   int  // index of first visible line
	follow   bool

	UpdateCh chan struct{}
	stopCh   chan struct{}
	fileSize int64
}

const maxLogLines = 5000

func NewLogViewer(path, name string, pid int) (*LogViewer, error) {
	lv := &LogViewer{
		FilePath: path,
		ProcName: name,
		PID:      pid,
		UpdateCh: make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
	}
	if err := lv.loadInitial(); err != nil {
		return nil, err
	}
	return lv, nil
}

func (lv *LogViewer) loadInitial() error {
	f, err := os.Open(lv.FilePath)
	if err != nil {
		return err
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		return err
	}

	fi, _ := f.Stat()
	if fi != nil {
		lv.fileSize = fi.Size()
	}

	allLines := strings.Split(string(content), "\n")
	if len(allLines) > maxLogLines {
		allLines = allLines[len(allLines)-maxLogLines:]
	}
	lv.lines = allLines
	return nil
}

// ContentLines returns the visible slice for rendering (displayH rows)
func (lv *LogViewer) ContentLines(displayH int) []string {
	lv.mu.Lock()
	defer lv.mu.Unlock()

	total := len(lv.lines)
	if total == 0 {
		return nil
	}
	start := lv.offset
	if start > total-1 {
		start = total - 1
	}
	end := start + displayH
	if end > total {
		end = total
	}
	return lv.lines[start:end]
}

func (lv *LogViewer) ScrollUp(n int) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	lv.offset -= n
	if lv.offset < 0 {
		lv.offset = 0
	}
}

func (lv *LogViewer) ScrollDown(n int) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	lv.offset += n
	total := len(lv.lines)
	if lv.offset >= total {
		lv.offset = total - 1
	}
	if lv.offset < 0 {
		lv.offset = 0
	}
}

func (lv *LogViewer) ScrollToBottom(displayH int) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	total := len(lv.lines)
	lv.offset = max(0, total-displayH)
}

func (lv *LogViewer) IsFollow() bool {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	return lv.follow
}

func (lv *LogViewer) ToggleFollow(displayH int) {
	lv.mu.Lock()
	wasFollow := lv.follow
	lv.follow = !lv.follow
	lv.mu.Unlock()

	if !wasFollow {
		// Starting follow: scroll to bottom and start goroutine
		lv.ScrollToBottom(displayH)
		go lv.followLoop()
	}
}

func (lv *LogViewer) followLoop() {
	f, err := os.Open(lv.FilePath)
	if err != nil {
		return
	}
	defer f.Close()

	// Start reading from current end
	lv.mu.Lock()
	size := lv.fileSize
	lv.mu.Unlock()

	f.Seek(size, io.SeekStart)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-lv.stopCh:
			return
		case <-ticker.C:
			lv.mu.Lock()
			following := lv.follow
			lv.mu.Unlock()
			if !following {
				return
			}

			fi, err := f.Stat()
			if err != nil {
				continue
			}
			newSize := fi.Size()
			if newSize <= size {
				continue
			}

			f.Seek(size, io.SeekStart)
			buf := make([]byte, newSize-size)
			n, err := f.Read(buf)
			if err != nil || n == 0 {
				continue
			}
			size = newSize

			newLines := strings.Split(string(buf[:n]), "\n")

			lv.mu.Lock()
			lv.lines = append(lv.lines, newLines...)
			if len(lv.lines) > maxLogLines {
				lv.lines = lv.lines[len(lv.lines)-maxLogLines:]
			}
			lv.fileSize = size
			lv.mu.Unlock()

			// Signal update
			select {
			case lv.UpdateCh <- struct{}{}:
			default:
			}
		}
	}
}

func (lv *LogViewer) Stop() {
	select {
	case <-lv.stopCh:
		// already closed
	default:
		close(lv.stopCh)
	}
}

func (lv *LogViewer) TotalLines() int {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	return len(lv.lines)
}

// Render draws the log view onto the terminal
func (lv *LogViewer) Render(t *Terminal, procName string, pid int) {
	w := t.Width
	h := t.Height

	Clear()

	// Header
	MoveTo(1, 1)
	followStr := ""
	if lv.follow {
		followStr = Green + " [FOLLOW]" + Reset
	}
	title := fmt.Sprintf(" Logs: %s (PID: %d)%s", procName, pid, followStr)
	keys := "  f:follow  ↑↓/jk:scroll  PgUp/PgDn  q:back"
	header := padRight(title+keys, w)
	fmt.Print(BgBlue + Bold + White + header + Reset)

	// File path
	MoveTo(2, 1)
	pathLine := padRight(fmt.Sprintf(" %s", lv.FilePath), w)
	fmt.Print(BgGray + Dim + pathLine + Reset)

	// Log content
	displayH := h - 3
	contentLines := lv.ContentLines(displayH)

	for i := 0; i < displayH; i++ {
		MoveTo(i+3, 1)
		if i < len(contentLines) {
			line := contentLines[i]
			// Colorize log level keywords
			colored := colorizeLine(line)
			fmt.Print(padRight(colored, w))
		} else {
			fmt.Print(strings.Repeat(" ", w))
		}
	}

	// Status bar
	MoveTo(h, 1)
	total := lv.TotalLines()
	statusLine := fmt.Sprintf(" %d lines  offset: %d", total, lv.offset)
	fmt.Print(BgBlack + White + padRight(statusLine, w) + Reset)
}

func colorizeLine(line string) string {
	lower := strings.ToLower(line)
	if strings.Contains(lower, " error ") || strings.Contains(lower, " error]") {
		return Red + line + Reset
	}
	if strings.Contains(lower, " warn ") || strings.Contains(lower, " warn]") {
		return Yellow + line + Reset
	}
	if strings.Contains(lower, " info ") || strings.Contains(lower, " info]") {
		return Green + line + Reset
	}
	if strings.Contains(lower, " debug ") || strings.Contains(lower, " debug]") {
		return Cyan + line + Reset
	}
	return line
}

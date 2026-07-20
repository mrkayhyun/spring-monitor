package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// tailReadBytes is the maximum number of bytes read from the tail of a log file
// on initial load. This prevents OOM for large log files.
const tailReadBytes = 2 * 1024 * 1024 // 2 MB

// LogViewer handles displaying and following a log file
type LogViewer struct {
	FilePath string
	ProcName string
	PID      int

	mu          sync.Mutex
	lines       []string
	offset      int // index of first visible line
	follow      bool
	pendingLine string // partial line accumulated across followLoop ticks

	// search state
	searchQuery   string
	searchActive  bool
	searchMatches []int // line indices that match the query
	searchIdx     int   // current match index within searchMatches (-1 = none)

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

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	fileSize := fi.Size()

	// Read at most tailReadBytes from the end of the file to avoid OOM on
	// large log files. fileSize tracks exactly how many bytes we consumed so
	// that followLoop can seek to the right position.
	readFrom := int64(0)
	if fileSize > tailReadBytes {
		readFrom = fileSize - tailReadBytes
		if _, err := f.Seek(readFrom, io.SeekStart); err != nil {
			return err
		}
	}

	content, err := io.ReadAll(f)
	if err != nil {
		return err
	}

	// fileSize must equal the byte offset after the last byte we read,
	// so followLoop can safely seek to it without missing or re-reading lines.
	lv.fileSize = readFrom + int64(len(content))

	allLines := strings.Split(string(content), "\n")
	// If we started mid-file, the first line is likely partial — drop it.
	if readFrom > 0 && len(allLines) > 1 {
		allLines = allLines[1:]
	}
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

			// Log rotation detection: file was truncated or replaced.
			if newSize < size {
				// Re-open the file to pick up the new inode (rename+create rotation)
				// or read from the beginning (copytruncate rotation).
				f.Close()
				newF, err := os.Open(lv.FilePath)
				if err != nil {
					continue
				}
				f = newF
				size = 0
				newSize = fi.Size()
			}

			if newSize <= size {
				continue
			}

			f.Seek(size, io.SeekStart)
			buf := make([]byte, newSize-size)
			n, err := f.Read(buf)
			if err != nil || n == 0 {
				continue
			}
			size = size + int64(n)

			chunk := string(buf[:n])

			lv.mu.Lock()
			// Prepend any partial line buffered from the previous tick so that
			// lines split across read boundaries are reassembled correctly.
			if lv.pendingLine != "" {
				chunk = lv.pendingLine + chunk
				lv.pendingLine = ""
			}
			newLines := strings.Split(chunk, "\n")
			// If the chunk doesn't end with '\n', the last element is a partial
			// line. Save it for the next tick instead of appending it now.
			if len(newLines) > 0 && buf[n-1] != '\n' {
				lv.pendingLine = newLines[len(newLines)-1]
				newLines = newLines[:len(newLines)-1]
			}

			startIdx := len(lv.lines)
			lv.lines = append(lv.lines, newLines...)
			if len(lv.lines) > maxLogLines {
				trimmed := len(lv.lines) - maxLogLines
				lv.lines = lv.lines[trimmed:]
				// Shift existing match indices; drop matches that were trimmed.
				droppedMatches := 0
				var kept []int
				for _, idx := range lv.searchMatches {
					if idx >= trimmed {
						kept = append(kept, idx-trimmed)
					} else {
						droppedMatches++
					}
				}
				lv.searchMatches = kept
				if lv.searchIdx >= 0 {
					lv.searchIdx -= droppedMatches
					if lv.searchIdx < 0 || lv.searchIdx >= len(kept) {
						lv.searchIdx = -1
					}
				}
				startIdx = max(0, startIdx-trimmed)
			}
			lv.fileSize = size
			// Extend search matches for newly appended lines
			if lv.searchActive && lv.searchQuery != "" {
				lower := strings.ToLower(lv.searchQuery)
				for i := startIdx; i < len(lv.lines); i++ {
					if strings.Contains(strings.ToLower(lv.lines[i]), lower) {
						lv.searchMatches = append(lv.searchMatches, i)
					}
				}
			}
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

func (lv *LogViewer) ScrollToTop() {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	lv.offset = 0
}

// SetSearch sets the search query and computes matching line indices.
func (lv *LogViewer) SetSearch(query string) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	lv.searchQuery = query
	lv.searchActive = query != ""
	lv.searchMatches = nil
	lv.searchIdx = -1
	if query == "" {
		return
	}
	lower := strings.ToLower(query)
	for i, line := range lv.lines {
		if strings.Contains(strings.ToLower(line), lower) {
			lv.searchMatches = append(lv.searchMatches, i)
		}
	}
}

// ClearSearch clears the current search.
func (lv *LogViewer) ClearSearch() {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	lv.searchQuery = ""
	lv.searchActive = false
	lv.searchMatches = nil
	lv.searchIdx = -1
}

// SearchNext moves to the next match and returns the line index (-1 if none).
func (lv *LogViewer) SearchNext() int {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if len(lv.searchMatches) == 0 {
		return -1
	}
	lv.searchIdx = (lv.searchIdx + 1) % len(lv.searchMatches)
	return lv.searchMatches[lv.searchIdx]
}

// SearchPrev moves to the previous match and returns the line index (-1 if none).
func (lv *LogViewer) SearchPrev() int {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if len(lv.searchMatches) == 0 {
		return -1
	}
	if lv.searchIdx < 0 {
		lv.searchIdx = len(lv.searchMatches) - 1
	} else {
		lv.searchIdx = (lv.searchIdx - 1 + len(lv.searchMatches)) % len(lv.searchMatches)
	}
	return lv.searchMatches[lv.searchIdx]
}

// SearchInfo returns (query, matchCount, currentMatch 1-based) for display.
func (lv *LogViewer) SearchInfo() (string, int, int) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if !lv.searchActive {
		return "", 0, 0
	}
	cur := 0
	if lv.searchIdx >= 0 {
		cur = lv.searchIdx + 1
	}
	return lv.searchQuery, len(lv.searchMatches), cur
}

// IsSearchActive returns true if a search query is active.
func (lv *LogViewer) IsSearchActive() bool {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	return lv.searchActive
}

// Render draws the log view onto the terminal
func (lv *LogViewer) Render(t *Terminal, procName string, pid int) {
	w := t.Width
	h := t.Height

	// Snapshot mutable state under the mutex
	lv.mu.Lock()
	follow := lv.follow
	offset := lv.offset
	total := len(lv.lines)
	searchActive := lv.searchActive
	searchQuery, searchCount, searchCur := lv.searchQuery, len(lv.searchMatches), 0
	if lv.searchIdx >= 0 {
		searchCur = lv.searchIdx + 1
	}
	// Build a set of match line indices for O(1) lookup
	matchSet := make(map[int]bool, len(lv.searchMatches))
	currentMatchLine := -1
	for _, idx := range lv.searchMatches {
		matchSet[idx] = true
	}
	if lv.searchIdx >= 0 && lv.searchIdx < len(lv.searchMatches) {
		currentMatchLine = lv.searchMatches[lv.searchIdx]
	}
	lv.mu.Unlock()

	Clear()

	// Header
	MoveTo(1, 1)
	followStr := ""
	if follow {
		followStr = Green + " [FOLLOW]" + Reset + BgBlue + Bold + White
	}
	title := fmt.Sprintf(" Logs: %s (PID: %d)%s", procName, pid, followStr)
	keys := "  f:follow  ↑↓/jk:scroll  PgUp/PgDn  /:search  n/N:next/prev  q:back"
	header := padRight(title+keys, w)
	fmt.Print(BgBlue + Bold + White + header + Reset)

	// File path / search bar
	MoveTo(2, 1)
	var subLine string
	if searchActive {
		subLine = fmt.Sprintf(" Search: %s%s%s  [%d/%d matches]", Bold, searchQuery, Reset+BgGray+Dim, searchCur, searchCount)
	} else {
		subLine = fmt.Sprintf(" %s", lv.FilePath)
	}
	fmt.Print(BgGray + Dim + padRight(subLine, w) + Reset)

	// Log content
	displayH := h - 3
	contentLines := lv.ContentLines(displayH)

	for i := 0; i < displayH; i++ {
		MoveTo(i+3, 1)
		if i < len(contentLines) {
			lineIdx := offset + i
			line := contentLines[i]
			var colored string
			if searchActive && lineIdx == currentMatchLine {
				colored = BgCyan + Bold + line + Reset
			} else if searchActive && matchSet[lineIdx] {
				colored = Yellow + Bold + line + Reset
			} else {
				colored = colorizeLine(line)
			}
			fmt.Print(padRight(colored, w))
		} else {
			fmt.Print(strings.Repeat(" ", w))
		}
	}

	// Status bar
	MoveTo(h, 1)
	var statusLine string
	if searchActive {
		statusLine = fmt.Sprintf(" %d lines  │  %d/%d matches for: \"%s\"", total, searchCur, searchCount, searchQuery)
	} else {
		statusLine = fmt.Sprintf(" %d lines  offset: %d", total, offset)
	}
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

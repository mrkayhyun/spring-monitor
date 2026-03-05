package ui

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// ANSI escape codes
const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Cyan    = "\033[36m"
	White   = "\033[37m"
	BgBlue  = "\033[44m"
	BgCyan  = "\033[46m"
	BgBlack = "\033[40m"
	BgGray  = "\033[100m"
)

// Special key codes (beyond ASCII range)
const (
	KeyUp    = 1000 + iota
	KeyDown
	KeyLeft
	KeyRight
	KeyPgUp
	KeyPgDn
	KeyHome
	KeyEnd
	KeyEsc   = 27
	KeyEnter = 13
	KeyCtrlC = 3
	KeyCtrlD = 4
)

// Terminal manages raw terminal state
type Terminal struct {
	fd       int
	oldState *term.State
	Width    int
	Height   int
}

func NewTerminal() *Terminal {
	return &Terminal{fd: int(os.Stdin.Fd())}
}

func (t *Terminal) MakeRaw() error {
	oldState, err := term.MakeRaw(t.fd)
	if err != nil {
		return err
	}
	t.oldState = oldState
	t.Refresh()
	return nil
}

func (t *Terminal) Restore() {
	if t.oldState != nil {
		term.Restore(t.fd, t.oldState)
	}
}

func (t *Terminal) Refresh() {
	w, h, err := term.GetSize(t.fd)
	if err != nil {
		t.Width = 80
		t.Height = 24
		return
	}
	t.Width = w
	t.Height = h
}

// Screen control helpers
func Clear() { fmt.Print("\033[2J\033[H") }

func MoveTo(row, col int) { fmt.Printf("\033[%d;%dH", row, col) }

func HideCursor() { fmt.Print("\033[?25l") }

func ShowCursor() { fmt.Print("\033[?25h") }

// ReadKey reads one keypress, returning ASCII value or a KeyXxx constant
func ReadKey() int {
	buf := make([]byte, 16)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return 0
	}
	if buf[0] == 27 {
		if n == 1 {
			return KeyEsc
		}
		if n >= 3 && buf[1] == '[' {
			switch buf[2] {
			case 'A':
				return KeyUp
			case 'B':
				return KeyDown
			case 'C':
				return KeyRight
			case 'D':
				return KeyLeft
			case 'H':
				return KeyHome
			case 'F':
				return KeyEnd
			case '5':
				return KeyPgUp
			case '6':
				return KeyPgDn
			}
		}
		return KeyEsc
	}
	return int(buf[0])
}

// visibleLen returns the display length of a string, ignoring ANSI codes
// and treating each rune as 1 column (adequate for ASCII + common Unicode)
func visibleLen(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
		} else if r == '\033' {
			inEsc = true
		} else {
			n++
		}
	}
	return n
}

// padRight pads s to exactly width visible characters (truncating if needed)
func padRight(s string, width int) string {
	vl := visibleLen(s)
	if vl >= width {
		// Truncate (strip ANSI for safety, then re-add)
		plain := stripAnsi(s)
		if len(plain) > width-3 && width > 3 {
			return plain[:width-3] + "..."
		}
		return plain[:min(len(plain), width)]
	}
	return s + strings.Repeat(" ", width-vl)
}

func stripAnsi(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
		} else if r == '\033' {
			inEsc = true
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 3 {
		return string(runes[:n])
	}
	return string(runes[:n-3]) + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

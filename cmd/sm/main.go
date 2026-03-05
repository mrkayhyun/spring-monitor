package main

import (
	"fmt"
	"os"

	"github.com/mrkayhyun/spring-monitor/internal/process"
	"github.com/mrkayhyun/spring-monitor/internal/ui"
)

var version = "dev"

func main() {
	// Scan for Spring processes
	procs, err := process.Scan()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning processes: %v\n", err)
		os.Exit(1)
	}

	// Initialize TUI
	app := ui.NewApp(version)
	if err := app.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing terminal: %v\n", err)
		os.Exit(1)
	}
	defer app.Cleanup()

	app.Run(procs)
}

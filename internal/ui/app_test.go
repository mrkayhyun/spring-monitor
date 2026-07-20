package ui

import (
	"reflect"
	"testing"
	"time"

	"github.com/mrkayhyun/spring-monitor/internal/process"
)

func TestSortProcessesUsesDeterministicTieBreakers(t *testing.T) {
	procs := []*process.SpringProcess{
		{PID: 3, Name: "charlie", MemoryMB: 128},
		{PID: 1, Name: "alpha", MemoryMB: 128},
		{PID: 2, Name: "bravo", MemoryMB: 128},
	}
	a := &App{sortBy: sortByMemory, sortDesc: true}

	a.sortProcesses(procs)

	got := []string{procs[0].Name, procs[1].Name, procs[2].Name}
	want := []string{"charlie", "bravo", "alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("descending sort with equal values = %v, want %v", got, want)
	}
}

func TestSortProcessesByUptime(t *testing.T) {
	now := time.Now()
	procs := []*process.SpringProcess{
		{Name: "old", StartTime: now.Add(-time.Hour)},
		{Name: "new", StartTime: now.Add(-time.Minute)},
	}
	a := &App{sortBy: sortByUptime}

	a.sortProcesses(procs)

	if procs[0].Name != "new" {
		t.Fatalf("shortest uptime process = %q, want new", procs[0].Name)
	}
}

func TestLogSearchStartsAtFirstMatch(t *testing.T) {
	lv := &LogViewer{lines: []string{"info", "first error", "second error"}}
	lv.SetSearch("ERROR")

	query, count, current := lv.SearchInfo()
	if query != "ERROR" || count != 2 || current != 0 {
		t.Fatalf("SearchInfo() = (%q, %d, %d), want (ERROR, 2, 0)", query, count, current)
	}
	if got := lv.SearchNext(); got != 1 {
		t.Fatalf("first SearchNext() = %d, want 1", got)
	}
	if got := lv.SearchNext(); got != 2 {
		t.Fatalf("second SearchNext() = %d, want 2", got)
	}
}

func TestLogSearchPrevStartsAtLastMatch(t *testing.T) {
	lv := &LogViewer{lines: []string{"first error", "info", "last error"}}
	lv.SetSearch("error")

	if got := lv.SearchPrev(); got != 2 {
		t.Fatalf("first SearchPrev() = %d, want 2", got)
	}
}

func TestSetProcessesPreservesSelectedPID(t *testing.T) {
	a := &App{
		processes: []*process.SpringProcess{
			{PID: 1, Name: "alpha", MemoryMB: 300},
			{PID: 2, Name: "bravo", MemoryMB: 100},
		},
		selected: 1,
		sortBy:   sortByMemory,
	}
	refreshed := []*process.SpringProcess{
		{PID: 1, Name: "alpha", MemoryMB: 50},
		{PID: 2, Name: "bravo", MemoryMB: 400},
	}

	a.setProcesses(refreshed)

	if got := a.processes[a.selected].PID; got != 2 {
		t.Fatalf("selected PID after refresh = %d, want 2", got)
	}
}

func TestManualSortPreservesSelectedPID(t *testing.T) {
	a := &App{
		processes: []*process.SpringProcess{
			{PID: 1, Name: "alpha"},
			{PID: 2, Name: "bravo"},
		},
		selected: 0,
		sortBy:   sortByName,
	}

	a.handleListKey('s')

	if got := a.processes[a.selected].PID; got != 1 {
		t.Fatalf("selected PID after sort = %d, want 1", got)
	}
}

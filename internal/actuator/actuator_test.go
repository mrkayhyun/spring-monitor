package actuator

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func metricBody(values ...map[string]any) map[string]any {
	return map[string]any{"measurements": values}
}

func TestCheckAndGracefulShutdown(t *testing.T) {
	shutdownCalled := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/actuator":
			writeJSON(t, w, map[string]any{"_links": map[string]any{
				"health":   map[string]any{"href": server.URL + "/actuator/health"},
				"shutdown": map[string]any{"href": server.URL + "/actuator/shutdown"},
			}})
		case "/actuator/health":
			writeJSON(t, w, map[string]any{"status": "UP"})
		case "/actuator/shutdown":
			if r.Method != http.MethodPost {
				t.Fatalf("shutdown method = %s, want POST", r.Method)
			}
			shutdownCalled = true
			writeJSON(t, w, map[string]any{"message": "shutting down"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	info, err := Check(server.URL + "/actuator")
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if !info.Available || !info.ShutdownAvailable || info.Health != "UP" {
		t.Fatalf("Check() = %+v", info)
	}
	if err := GracefulShutdown(server.URL + "/actuator/"); err != nil {
		t.Fatalf("GracefulShutdown() error: %v", err)
	}
	if !shutdownCalled {
		t.Fatal("shutdown endpoint was not called")
	}
}

func TestGetMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/actuator/metrics/http.server.requests" {
			if r.URL.Query().Get("tag") == "outcome:SERVER_ERROR" {
				writeJSON(t, w, metricBody(map[string]any{"statistic": "COUNT", "value": 2}))
				return
			}
			writeJSON(t, w, metricBody(
				map[string]any{"statistic": "COUNT", "value": 10},
				map[string]any{"statistic": "TOTAL_TIME", "value": 1.5},
			))
			return
		}

		value := float64(0)
		switch r.URL.Path {
		case "/actuator/metrics/jvm.memory.used":
			if r.URL.Query().Get("tag") == "area:heap" {
				value = 100 * 1048576
			} else {
				value = 50 * 1048576
			}
		case "/actuator/metrics/jvm.memory.max":
			value = 256 * 1048576
		case "/actuator/metrics/jvm.threads.live":
			value = 42
		default:
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, metricBody(map[string]any{"statistic": "VALUE", "value": value}))
	}))
	defer server.Close()

	metrics, err := GetMetrics(server.URL + "/actuator/")
	if err != nil {
		t.Fatalf("GetMetrics() error: %v", err)
	}
	if !metrics.Available || metrics.HeapUsedMB != 100 || metrics.HeapMaxMB != 256 || metrics.NonHeapMB != 50 {
		t.Fatalf("memory metrics = %+v", metrics)
	}
	if metrics.ThreadCount != 42 || metrics.HTTPRequestCount != 10 || metrics.HTTPErrorCount != 2 {
		t.Fatalf("count metrics = %+v", metrics)
	}
	if math.Abs(metrics.HTTPAvgDurationMs-150) > 0.001 {
		t.Fatalf("HTTPAvgDurationMs = %f, want 150", metrics.HTTPAvgDurationMs)
	}
}

func TestGetMetricsReturnsErrorWhenNotExposed(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	metrics, err := GetMetrics(server.URL + "/actuator")
	if err == nil || metrics != nil {
		t.Fatalf("GetMetrics() = (%+v, %v), want (nil, error)", metrics, err)
	}
}

func TestGetInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/actuator/info" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"build": map[string]any{"version": "1.2.3", "time": "2026-07-20T00:00:00Z"},
			"git": map[string]any{
				"branch": "main",
				"commit": map[string]any{"id": "abcdef123456"},
			},
			"environment": "production",
		})
	}))
	defer server.Close()

	info, err := GetInfo(server.URL + "/actuator/")
	if err != nil {
		t.Fatalf("GetInfo() error: %v", err)
	}
	if info.AppVersion != "1.2.3" || info.GitBranch != "main" || info.GitCommit != "abcdef123456" {
		t.Fatalf("GetInfo() = %+v", info)
	}
	if info.Extra["environment"] != "production" {
		t.Fatalf("GetInfo().Extra = %+v", info.Extra)
	}
}

func TestGetMetricsTracksPartialAvailability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/actuator/metrics/jvm.threads.live" {
			writeJSON(t, w, metricBody(map[string]any{"statistic": "VALUE", "value": 7}))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	metrics, err := GetMetrics(server.URL + "/actuator")
	if err != nil {
		t.Fatalf("GetMetrics() error: %v", err)
	}
	if !metrics.Available || !metrics.ThreadsAvailable || metrics.ThreadCount != 7 {
		t.Fatalf("thread metrics = %+v", metrics)
	}
	if metrics.HeapUsedAvailable || metrics.NonHeapAvailable || metrics.HTTPAvailable {
		t.Fatalf("unexposed metrics marked available: %+v", metrics)
	}
}

func TestGetInfoRejectsNonSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(t, w, map[string]any{"error": "internal server error"})
	}))
	defer server.Close()

	info, err := GetInfo(server.URL + "/actuator")
	if err == nil || info != nil {
		t.Fatalf("GetInfo() = (%+v, %v), want (nil, error)", info, err)
	}
}

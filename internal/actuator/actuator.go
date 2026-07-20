package actuator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

var client = &http.Client{
	Timeout: 2 * time.Second,
}

// Info contains discovered actuator information
type Info struct {
	Available         bool
	ShutdownAvailable bool
	Health            string
	Endpoints         []string
}

type linksResponse struct {
	Links map[string]struct {
		Href      string `json:"href"`
		Templated bool   `json:"templated"`
	} `json:"_links"`
}

type healthResponse struct {
	Status string `json:"status"`
}

// Check tests if Spring Actuator is reachable and gathers endpoint info
func Check(baseURL string) (*Info, error) {
	resp, err := client.Get(baseURL)
	if err != nil {
		return &Info{Available: false}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &Info{Available: false}, nil
	}

	var links linksResponse
	if err := json.NewDecoder(resp.Body).Decode(&links); err != nil {
		return &Info{Available: true}, nil
	}

	info := &Info{Available: true}
	for name := range links.Links {
		info.Endpoints = append(info.Endpoints, name)
		if name == "shutdown" {
			info.ShutdownAvailable = true
		}
	}
	sort.Strings(info.Endpoints)

	// Probe health endpoint
	if h, ok := links.Links["health"]; ok && !h.Templated {
		if hResp, err := client.Get(h.Href); err == nil {
			defer hResp.Body.Close()
			var health healthResponse
			if json.NewDecoder(hResp.Body).Decode(&health) == nil {
				info.Health = health.Status
			}
		}
	}

	return info, nil
}

func endpointURL(baseURL, path string) string {
	if len(baseURL) > 0 && baseURL[len(baseURL)-1] == '/' {
		baseURL = baseURL[:len(baseURL)-1]
	}
	return baseURL + path
}

// GracefulShutdown sends POST /actuator/shutdown
func GracefulShutdown(baseURL string) error {
	resp, err := client.Post(endpointURL(baseURL, "/shutdown"), "application/json", nil)
	if err != nil {
		return fmt.Errorf("shutdown request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shutdown returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Metrics holds JVM and HTTP request metrics collected from Spring Actuator.
type Metrics struct {
	Available          bool
	HeapUsedAvailable  bool
	HeapMaxAvailable   bool
	NonHeapAvailable   bool
	ThreadsAvailable   bool
	HTTPAvailable      bool
	HTTPErrorAvailable bool
	HeapUsedMB         float64
	HeapMaxMB          float64
	NonHeapMB          float64
	ThreadCount        int
	HTTPRequestCount   int64
	HTTPErrorCount     int64
	HTTPAvgDurationMs  float64
}

type metricResponse struct {
	Measurements []struct {
		Statistic string  `json:"statistic"`
		Value     float64 `json:"value"`
	} `json:"measurements"`
}

func getMetric(baseURL, metricName, tag string) (*metricResponse, error) {
	url := endpointURL(baseURL, "/metrics/"+metricName)
	if tag != "" {
		url += "?tag=" + tag
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics/%s returned HTTP %d", metricName, resp.StatusCode)
	}
	var metric metricResponse
	if err := json.NewDecoder(resp.Body).Decode(&metric); err != nil {
		return nil, err
	}
	if len(metric.Measurements) == 0 {
		return nil, fmt.Errorf("metrics/%s: no measurements", metricName)
	}
	return &metric, nil
}

func measurement(metric *metricResponse, statistic string) (float64, bool) {
	for _, item := range metric.Measurements {
		if item.Statistic == statistic {
			return item.Value, true
		}
	}
	return 0, false
}

func getMetricValue(baseURL, metricName, tag string) (float64, error) {
	metric, err := getMetric(baseURL, metricName, tag)
	if err != nil {
		return 0, err
	}
	if value, ok := measurement(metric, "VALUE"); ok {
		return value, nil
	}
	return metric.Measurements[0].Value, nil
}

// GetMetrics fetches JVM memory, thread, and HTTP request metrics from the actuator.
// Missing individual metrics are tolerated, but an error is returned when none of
// the supported metrics are exposed.
func GetMetrics(baseURL string) (*Metrics, error) {
	m := &Metrics{}
	fetched := 0
	if v, err := getMetricValue(baseURL, "jvm.memory.used", "area:heap"); err == nil {
		m.HeapUsedMB = v / 1048576
		m.HeapUsedAvailable = true
		fetched++
	}
	if v, err := getMetricValue(baseURL, "jvm.memory.max", "area:heap"); err == nil {
		m.HeapMaxMB = v / 1048576
		m.HeapMaxAvailable = true
		fetched++
	}
	if v, err := getMetricValue(baseURL, "jvm.memory.used", "area:nonheap"); err == nil {
		m.NonHeapMB = v / 1048576
		m.NonHeapAvailable = true
		fetched++
	}
	if v, err := getMetricValue(baseURL, "jvm.threads.live", ""); err == nil {
		m.ThreadCount = int(v)
		m.ThreadsAvailable = true
		fetched++
	}
	if metric, err := getMetric(baseURL, "http.server.requests", ""); err == nil {
		fetched++
		m.HTTPAvailable = true
		count, _ := measurement(metric, "COUNT")
		totalTime, _ := measurement(metric, "TOTAL_TIME")
		m.HTTPRequestCount = int64(count)
		if count > 0 {
			m.HTTPAvgDurationMs = (totalTime / count) * 1000
		}
	}
	if metric, err := getMetric(baseURL, "http.server.requests", "outcome:SERVER_ERROR"); err == nil {
		if count, ok := measurement(metric, "COUNT"); ok {
			m.HTTPErrorCount = int64(count)
			m.HTTPErrorAvailable = true
		}
	}
	if fetched == 0 {
		return nil, fmt.Errorf("no supported actuator metrics are exposed")
	}
	m.Available = true
	return m, nil
}

// AppInfo holds application metadata from the Spring Boot /info endpoint.
type AppInfo struct {
	AppVersion string
	BuildTime  string
	GitBranch  string
	GitCommit  string
	Extra      map[string]string
}

func extractString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch s := v.(type) {
			case string:
				return s
			case float64:
				return fmt.Sprintf("%g", s)
			}
		}
	}
	return ""
}

func nestedMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key]; ok {
		if sub, ok := v.(map[string]any); ok {
			return sub
		}
	}
	return nil
}

// GetInfo fetches application metadata from the Spring Boot /info endpoint.
func GetInfo(baseURL string) (*AppInfo, error) {
	resp, err := client.Get(endpointURL(baseURL, "/info"))
	if err != nil {
		return nil, fmt.Errorf("info request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return &AppInfo{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("info returned HTTP %d", resp.StatusCode)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode info response: %w", err)
	}
	if len(raw) == 0 {
		return &AppInfo{}, nil
	}
	info := &AppInfo{Extra: make(map[string]string)}
	if build := nestedMap(raw, "build"); build != nil {
		info.AppVersion = extractString(build, "version")
		info.BuildTime = extractString(build, "time")
	}
	if info.AppVersion == "" {
		if app := nestedMap(raw, "app"); app != nil {
			info.AppVersion = extractString(app, "version")
		}
	}
	if git := nestedMap(raw, "git"); git != nil {
		info.GitBranch = extractString(git, "branch")
		if commit := nestedMap(git, "commit"); commit != nil {
			info.GitCommit = extractString(commit, "id.abbrev", "id")
		} else {
			info.GitCommit = extractString(git, "commit.id.abbrev", "commit.id")
		}
	}
	knownTopLevel := map[string]bool{"build": true, "git": true, "app": true}
	for k, v := range raw {
		if knownTopLevel[k] {
			continue
		}
		switch s := v.(type) {
		case string:
			info.Extra[k] = s
		case float64:
			info.Extra[k] = fmt.Sprintf("%g", s)
		}
	}
	return info, nil
}

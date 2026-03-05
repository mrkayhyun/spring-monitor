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

// GracefulShutdown sends POST /actuator/shutdown
func GracefulShutdown(baseURL string) error {
	resp, err := client.Post(baseURL+"/shutdown", "application/json", nil)
	if err != nil {
		return fmt.Errorf("shutdown request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shutdown returned HTTP %d", resp.StatusCode)
	}
	return nil
}

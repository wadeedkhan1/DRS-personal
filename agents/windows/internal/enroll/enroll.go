// Package enroll exchanges a one-time enrollment token for this device's permanent
// identity.
package enroll

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"drs/agent/windows/internal/config"
	"drs/agent/windows/internal/sysinfo"
)

type enrollRequest struct {
	EnrollmentToken string `json:"enrollment_token"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	OSVersion       string `json:"os_version"`
}

type enrollResponse struct {
	DeviceID                 string `json:"deviceId"`
	AgentSecret              string `json:"agentSecret"`
	WSURL                    string `json:"wsUrl"`
	HeartbeatIntervalSeconds int    `json:"heartbeatIntervalSeconds"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Enroll redeems a token and returns the identity to persist. deviceName may be empty,
// in which case the machine's hostname is used.
func Enroll(serverURL, token, deviceName string) (config.Config, error) {
	serverURL = strings.TrimRight(serverURL, "/")
	if deviceName == "" {
		if h, err := os.Hostname(); err == nil {
			deviceName = h
		} else {
			deviceName = "Unnamed device"
		}
	}

	body, err := json.Marshal(enrollRequest{
		EnrollmentToken: token,
		Name:            deviceName,
		Type:            runtime.GOOS,
		OSVersion:       sysinfo.Info().OS,
	})
	if err != nil {
		return config.Config{}, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(serverURL+"/api/devices/enroll", "application/json", bytes.NewReader(body))
	// Check err before touching resp. The previous agent read resp.StatusCode in the
	// same branch as a non-nil error, which panicked on a nil resp every time the
	// server was unreachable.
	if err != nil {
		return config.Config{}, fmt.Errorf("could not reach %s: %w", serverURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var er errorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		if er.Error != "" {
			return config.Config{}, fmt.Errorf("enrollment refused (%d): %s", resp.StatusCode, er.Error)
		}
		return config.Config{}, fmt.Errorf("enrollment refused with status %d", resp.StatusCode)
	}

	var out enrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return config.Config{}, fmt.Errorf("could not read enrollment response: %w", err)
	}
	if out.DeviceID == "" || out.AgentSecret == "" {
		return config.Config{}, fmt.Errorf("enrollment response was missing the device identity")
	}

	cfg := config.Config{
		ServerURL:                serverURL,
		WSURL:                    out.WSURL,
		DeviceID:                 out.DeviceID,
		AgentSecret:              out.AgentSecret,
		HeartbeatIntervalSeconds: out.HeartbeatIntervalSeconds,
	}
	if cfg.WSURL == "" {
		cfg.WSURL = config.DeriveWSURL(serverURL)
	}
	if cfg.HeartbeatIntervalSeconds <= 0 {
		cfg.HeartbeatIntervalSeconds = 10
	}
	return cfg, nil
}

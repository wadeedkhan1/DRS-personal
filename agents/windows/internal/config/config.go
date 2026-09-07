// Package config persists the agent's identity between runs.
//
// The file holds the agent secret, which is a long-lived credential for this device,
// so it is written 0600 into the user's config directory rather than next to the
// executable. The previous agent wrote it world-readable into the working directory.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the on-disk identity.
type Config struct {
	ServerURL                string `json:"serverUrl"`
	WSURL                    string `json:"wsUrl"`
	DeviceID                 string `json:"deviceId"`
	AgentSecret              string `json:"agentSecret"`
	HeartbeatIntervalSeconds int    `json:"heartbeatIntervalSeconds"`

	// The capabilities requested at enrollment. This build always requests both, so these
	// are written true; they are kept because configs written by older builds may say
	// otherwise, and because the server's copy — not this one — is what is enforced. The
	// agent never reads these to decide whether to obey a command.
	AllowScreen   bool `json:"allowScreen"`
	AllowTerminal bool `json:"allowTerminal"`
}

// Enrolled reports whether this config can actually connect.
func (c Config) Enrolled() bool {
	return c.DeviceID != "" && c.AgentSecret != "" && c.WSURL != ""
}

// DefaultPath is where the identity lives: %AppData%\drs\agent.json on Windows.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "drs", "agent.json"), nil
}

// Load reads the identity. A missing file is not an error; it returns a zero Config
// whose Enrolled() is false, which is how the CLI decides to ask for a token.
func Load() (Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("agent config at %s is corrupt: %w", path, err)
	}
	if cfg.HeartbeatIntervalSeconds <= 0 {
		cfg.HeartbeatIntervalSeconds = 10
	}
	return cfg, nil
}

// Save writes the identity with owner-only permissions.
func Save(cfg Config) error {
	path, err := DefaultPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// DeriveWSURL turns a server base URL into the agent socket URL. Used when the server
// did not tell us where to connect, which can happen behind a proxy that rewrites Host.
func DeriveWSURL(serverURL string) string {
	base := strings.TrimRight(serverURL, "/")
	switch {
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + strings.TrimPrefix(base, "http://")
	}
	return base + "/ws/agent"
}

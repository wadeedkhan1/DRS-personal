package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config is the deployment's settings, all of it from the environment.
type Config struct {
	Port       string
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	JWTSecret string

	// AllowedOrigins is the explicit list of browser origins permitted to call the
	// API and open a WebSocket. "*" is accepted for local development only and is
	// rejected together with credentials (see middleware.CORS).
	AllowedOrigins []string

	// HeartbeatInterval is how often an agent must beat. The socket read deadline is
	// three times this, so it also decides how fast a dead device is noticed.
	HeartbeatInterval time.Duration

	// SessionFPS and SessionMaxWidth are what the backend asks agents to capture at.
	// Exposed as configuration because the right values depend on the endpoint hardware,
	// and finding them is a matter of trying a few, which should not need a rebuild.
	SessionFPS      int
	SessionMaxWidth int

	// NAT traversal, handed identically to both peers of a session.
	STUNURLs     []string
	TURNPublicIP string
	TURNPort     int
	TURNSecret   string
	TURNRealm    string
	TURNCredTTL  time.Duration

	// ForceRelay routes all session media through the TURN relay rather than only
	// using it as a fallback. Requires TURN to be configured.
	ForceRelay bool

	// AgentBinaryDir is where the agent binaries staged for download live
	// (drs-agent.exe, drs-agent.apk). Empty disables the personalised-download endpoint
	// entirely, which is the right default: a deployment that has not staged a binary
	// should say so rather than serve a 500 or an empty file.
	//
	// This is the only filesystem path the backend knows about. nginx keeps serving the
	// same directory at /downloads/ for the plain, unconfigured binaries.
	AgentBinaryDir string

	// PublicBaseURL overrides the origin the backend believes it is reachable at, which
	// is otherwise derived per-request from X-Forwarded-Proto / X-Forwarded-Host / Host.
	// That derivation is right behind nginx and right in most dev setups; this exists for
	// the ones where it is not, because the value gets baked into downloaded agents and a
	// wrong one produces agents that can never connect.
	PublicBaseURL string

	// Bootstrap super admin, created only when no super admin exists yet.
	AdminEmail string
	AdminPass  string
}

// LoadConfig reads configuration from the environment (and a .env file if present).
//
// It returns an error rather than falling back to a built-in default for anything
// that is a secret. The previous version shipped a hardcoded JWT signing key and
// admin password as defaults, which meant a deployment that simply forgot to set them
// came up with publicly-known credentials and no warning. Failing to start is the
// safer outcome.
func LoadConfig() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		Port:       getEnv("PORT", "8080"),
		DBHost:     getEnv("DB_HOST", "127.0.0.1"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     getEnv("DB_NAME", "drs_db"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),

		JWTSecret:         os.Getenv("JWT_SECRET"),
		AllowedOrigins:    splitList(getEnv("CORS_ORIGINS", "http://localhost:3000")),
		HeartbeatInterval: time.Duration(getEnvInt("HEARTBEAT_SECONDS", 10)) * time.Second,
		SessionFPS:        getEnvInt("SESSION_FPS", 24),
		SessionMaxWidth:   getEnvInt("SESSION_MAX_WIDTH", 1280),

		STUNURLs:     splitList(getEnv("STUN_URLS", "stun:stun.l.google.com:19302")),
		TURNPublicIP: os.Getenv("TURN_PUBLIC_IP"),
		TURNPort:     getEnvInt("TURN_PORT", 3478),
		TURNSecret:   os.Getenv("TURN_SHARED_SECRET"),
		TURNRealm:    getEnv("TURN_REALM", "drs"),
		TURNCredTTL:  time.Duration(getEnvInt("TURN_CRED_TTL_SECONDS", 3600)) * time.Second,
		ForceRelay:   getEnvBool("FORCE_TURN_RELAY", false),

		AgentBinaryDir: os.Getenv("AGENT_BINARY_DIR"),
		PublicBaseURL:  strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),

		AdminEmail: getEnv("DEFAULT_SUPERADMIN_EMAIL", "admin@drs.local"),
		AdminPass:  os.Getenv("DEFAULT_SUPERADMIN_PASSWORD"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.JWTSecret) == "" {
		return errors.New("JWT_SECRET is required (set it to a long random string; " +
			"every portal session is signed with it)")
	}
	// 32 bytes is the floor for HMAC-SHA256 to actually carry 256 bits of strength.
	if len(c.JWTSecret) < 32 {
		return errors.New("JWT_SECRET must be at least 32 characters")
	}
	if strings.TrimSpace(c.DBPassword) == "" {
		return errors.New("DB_PASSWORD is required")
	}
	if c.HeartbeatInterval < time.Second {
		return errors.New("HEARTBEAT_SECONDS must be at least 1")
	}
	// Refusing to start beats starting into a configuration where every single session
	// fails to connect for a reason nothing reports.
	if c.ForceRelay && (c.TURNPublicIP == "" || c.TURNSecret == "") {
		return errors.New("FORCE_TURN_RELAY requires TURN_PUBLIC_IP and TURN_SHARED_SECRET: " +
			"forcing relay without a TURN server means no session can ever connect")
	}
	return nil
}

// DatabaseURL builds the lib/pq connection string.
func (c *Config) DatabaseURL() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode)
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
	}
	return defaultVal
}

// splitList parses a comma-separated env value, dropping blanks.
func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

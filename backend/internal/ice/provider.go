// Package ice builds the ICE server list handed to both peers of a session.
//
// Both sides must be given the *same* list, or they negotiate against different
// candidate universes and the connection fails in a way that is very hard to read
// from either end. So there is one provider: the browser fetches the list from
// GET /api/session/ice, and the agent receives an identical copy inside StartSession.
package ice

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"drs/backend/pkg/protocol"
)

// Config is the deployment's NAT-traversal setup.
type Config struct {
	// STUNURLs lets peers discover their own public address. Free and stateless,
	// but it only helps when a direct path exists at all.
	STUNURLs []string

	// TURN relays media when no direct path exists, which is the common case behind
	// the symmetric NAT and corporate firewalls this product is aimed at (SDS 3.1).
	// Empty PublicIP or Secret disables it.
	TURNPublicIP string
	TURNPort     int
	TURNSecret   string
	TURNRealm    string
	TURNCredTTL  time.Duration

	// ForceRelay makes every session use TURN even when the peers could reach each
	// other directly. It trades bandwidth for predictability: all media flows through
	// the server, so connectivity no longer depends on either endpoint's NAT.
	ForceRelay bool
}

// Provider hands out ICE server lists.
type Provider struct {
	cfg Config
	now func() time.Time // injectable for tests
}

// NewProvider builds a Provider from config, applying defaults.
func NewProvider(cfg Config) *Provider {
	if cfg.TURNPort == 0 {
		cfg.TURNPort = 3478
	}
	if cfg.TURNCredTTL == 0 {
		cfg.TURNCredTTL = time.Hour
	}
	if len(cfg.STUNURLs) == 0 {
		cfg.STUNURLs = []string{"stun:stun.l.google.com:19302"}
	}
	return &Provider{cfg: cfg, now: time.Now}
}

// TransportPolicy is what both peers should set as their ICE transport policy.
//
// Returning "relay" without a configured TURN server would produce sessions that can
// never connect, so it is gated on the relay actually existing.
func (p *Provider) TransportPolicy() string {
	if p.cfg.ForceRelay && p.TURNEnabled() {
		return "relay"
	}
	return "all"
}

// TURNEnabled reports whether a relay is configured.
func (p *Provider) TURNEnabled() bool {
	return p.cfg.TURNPublicIP != "" && p.cfg.TURNSecret != ""
}

// Servers returns the list for one session. TURN credentials are minted fresh per
// call and expire, so a list leaked from a browser's memory stops working.
func (p *Provider) Servers() []protocol.ICEServer {
	out := make([]protocol.ICEServer, 0, 2)
	if len(p.cfg.STUNURLs) > 0 {
		out = append(out, protocol.ICEServer{URLs: p.cfg.STUNURLs})
	}
	if !p.TURNEnabled() {
		return out
	}

	// The coturn REST scheme (draft-uberti-behave-turn-rest): the username is an
	// expiry timestamp and the password is an HMAC of it under a secret shared with
	// the TURN server. No per-user state anywhere, and credentials age out on their
	// own.
	username := strconv.FormatInt(p.now().Add(p.cfg.TURNCredTTL).Unix(), 10)
	credential := RESTCredential(p.cfg.TURNSecret, username)

	host := p.cfg.TURNPublicIP + ":" + strconv.Itoa(p.cfg.TURNPort)
	out = append(out, protocol.ICEServer{
		URLs: []string{
			"turn:" + host + "?transport=udp",
			// TCP as well: some corporate networks drop UDP outright, which is
			// exactly the situation TURN is here to rescue.
			"turn:" + host + "?transport=tcp",
		},
		Username:   username,
		Credential: credential,
	})
	return out
}

// ParseSTUNURLs splits a comma-separated env value, ignoring blanks.
func ParseSTUNURLs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// RESTCredential derives the TURN password for a REST-scheme username.
//
// This is the single definition of that derivation. cmd/turnserver validates incoming
// credentials by calling this same function, so the relay and the list handed to peers
// cannot drift apart. They previously could: a mismatch here is invisible from both
// ends — the browser reports only that ICE failed, and the relay reports only an
// unauthorised request — which makes it one of the most expensive bugs in this area to
// find by inspection.
func RESTCredential(secret, username string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// ParseRESTUsername extracts the expiry from a REST-scheme username.
//
// Two forms are accepted, both from the same draft: a bare unix timestamp, and
// "<timestamp>:<userid>" as coturn emits. Only the timestamp is meaningful here —
// possession of a valid HMAC is the authorisation, so the id is informational.
func ParseRESTUsername(username string) (expiry time.Time, ok bool) {
	ts := username
	if i := strings.IndexByte(username, ':'); i >= 0 {
		ts = username[:i]
	}
	secs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

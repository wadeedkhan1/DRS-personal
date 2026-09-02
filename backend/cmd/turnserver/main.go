// Command turnserver is the DRS media relay.
//
// It exists so that every session's video can be made to travel through the server
// rather than peer-to-peer. WebRTC will always prefer a direct path; setting
// FORCE_TURN_RELAY=true tells both peers to discard host and server-reflexive
// candidates, which leaves this relay as the only way for media to move. Connectivity
// then stops depending on either endpoint's NAT: the agent and the browser each make an
// *outbound* allocation here, so neither needs a public address, an open inbound port,
// or any cooperation from its router. That is what makes a device on a home or corporate
// network reachable from anywhere.
//
// Why this rather than coturn: the credentials are the whole contract between the relay
// and the rest of the system, and here both sides are literally the same function
// (ice.RESTCredential). A format mismatch between an external relay and the ICE list is
// invisible from both ends — the browser reports only that ICE failed, the relay only
// that a request was unauthorised — so removing the possibility is worth more than the
// features coturn has and this does not. It also runs anywhere Go runs, which means
// forced relay can be tested locally instead of only after deployment.
//
// It is a separate binary from the API server because it wants the host's network
// directly: a relay hands out a port per allocation, and publishing a range of them
// through a container bridge costs a proxy process per port.
//
// Media is relayed, not inspected. DTLS-SRTP is negotiated end to end between the agent
// and the browser, so this process forwards packets it cannot read — it carries the
// stream without becoming a party to it.
package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/pion/turn/v4"

	"drs/backend/internal/ice"
)

func main() {
	log.SetFlags(log.LstdFlags)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("[TURN] %v", err)
	}

	server, err := start(cfg)
	if err != nil {
		log.Fatalf("[TURN] %v", err)
	}

	log.Printf("[TURN] relay listening on %s:%d (udp+tcp), realm %q, relay address %s, ports %d-%d",
		cfg.ListenAddr, cfg.Port, cfg.Realm, cfg.PublicIP, cfg.MinPort, cfg.MaxPort)
	log.Printf("[TURN] set these on the backend so it hands out matching credentials: " +
		"TURN_PUBLIC_IP, TURN_SHARED_SECRET, TURN_PORT, TURN_REALM")

	// Wait for a signal rather than blocking forever, so a container stop closes the
	// listeners and frees the relay allocations instead of being killed outright.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	log.Printf("[TURN] shutting down")
	if err := server.Close(); err != nil {
		log.Printf("[TURN] close: %v", err)
	}
}

// config is the relay's runtime configuration. Every field is deliberately the same
// environment variable the backend reads, so one .env drives both.
type config struct {
	PublicIP   string
	ListenAddr string
	Port       int
	Realm      string
	Secret     string
	MinPort    uint16
	MaxPort    uint16
}

func loadConfig() (config, error) {
	// Same .env the API server reads. Loading it here is what lets a local
	// forced-relay test be "go run ./cmd/turnserver" with no environment to set up
	// twice -- and it removes the chance of the two processes being pointed at
	// different secrets, which fails as an unauthorised request with no hint as to why.
	_ = godotenv.Load()

	c := config{
		PublicIP:   os.Getenv("TURN_PUBLIC_IP"),
		ListenAddr: envOr("TURN_LISTEN_ADDR", "0.0.0.0"),
		Port:       envInt("TURN_PORT", 3478),
		Realm:      envOr("TURN_REALM", "drs"),
		Secret:     os.Getenv("TURN_SHARED_SECRET"),
		MinPort:    uint16(envInt("TURN_MIN_PORT", 49160)),
		MaxPort:    uint16(envInt("TURN_MAX_PORT", 49200)),
	}

	// Both of these are fatal rather than defaulted. A relay with no secret would
	// accept anyone's traffic and become an open proxy; a relay that does not know its
	// own public address hands out candidates pointing at a private one, which fails
	// only later, only for remote peers, and looks exactly like a firewall problem.
	if c.Secret == "" {
		return c, errors.New("TURN_SHARED_SECRET is required: without it the relay would " +
			"accept credentials from anyone and act as an open relay")
	}
	if c.PublicIP == "" {
		return c, errors.New("TURN_PUBLIC_IP is required: it is the address the relay puts " +
			"in the candidates it hands out, so a private or empty value produces " +
			"candidates no remote peer can reach")
	}
	if net.ParseIP(c.PublicIP) == nil {
		return c, fmt.Errorf("TURN_PUBLIC_IP must be an IP address, not a hostname: %q", c.PublicIP)
	}
	if c.MinPort > c.MaxPort {
		return c, fmt.Errorf("TURN_MIN_PORT (%d) is above TURN_MAX_PORT (%d)", c.MinPort, c.MaxPort)
	}
	return c, nil
}

func start(cfg config) (*turn.Server, error) {
	addr := net.JoinHostPort(cfg.ListenAddr, strconv.Itoa(cfg.Port))

	udpListener, err := net.ListenPacket("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("listen udp %s: %w", addr, err)
	}

	// TCP as well as UDP. Networks that drop UDP wholesale are exactly the ones a relay
	// is here to rescue, and for them a TCP allocation is the only path that works.
	tcpListener, err := net.Listen("tcp4", addr)
	if err != nil {
		udpListener.Close() //nolint:errcheck
		return nil, fmt.Errorf("listen tcp %s: %w", addr, err)
	}

	relay := &turn.RelayAddressGeneratorPortRange{
		// RelayAddress is what peers are told to send to; Address is what the process
		// binds. They differ whenever the host is NATed (most cloud providers), and
		// conflating them is the classic cause of a relay that allocates successfully
		// and then receives nothing.
		RelayAddress: net.ParseIP(cfg.PublicIP),
		Address:      cfg.ListenAddr,
		MinPort:      cfg.MinPort,
		MaxPort:      cfg.MaxPort,
	}

	server, err := turn.NewServer(turn.ServerConfig{
		Realm:       cfg.Realm,
		AuthHandler: authHandler(cfg.Secret, cfg.Realm),
		PacketConnConfigs: []turn.PacketConnConfig{
			{PacketConn: udpListener, RelayAddressGenerator: relay},
		},
		ListenerConfigs: []turn.ListenerConfig{
			{Listener: tcpListener, RelayAddressGenerator: relay},
		},
	})
	if err != nil {
		udpListener.Close() //nolint:errcheck
		tcpListener.Close() //nolint:errcheck
		return nil, fmt.Errorf("start turn server: %w", err)
	}
	return server, nil
}

// authHandler validates a REST-scheme credential.
//
// There are no accounts here. The username is an expiry timestamp and the password is an
// HMAC of that timestamp under the shared secret, so the relay can verify a credential
// it has never seen, issued by a backend it never talks to, and refuse it once it ages
// out. That is what keeps the relay stateless and lets it be restarted or scaled without
// touching the API server.
//
// Returning the long-term-credential key (MD5 of username:realm:password) is what
// turn.NewServer expects; GenerateAuthKey builds it in the form the STUN
// MESSAGE-INTEGRITY check needs.
func authHandler(secret, realm string) turn.AuthHandler {
	return func(username, _ string, srcAddr net.Addr) ([]byte, bool) {
		expiry, ok := ice.ParseRESTUsername(username)
		if !ok {
			log.Printf("[TURN] rejected %s: username %q is not a REST-scheme timestamp",
				srcAddr, username)
			return nil, false
		}
		if time.Now().After(expiry) {
			// Worth logging distinctly: an expired credential means a session was held
			// open past the TTL, which is a configuration question (TURN_CRED_TTL_SECONDS),
			// not an attack.
			log.Printf("[TURN] rejected %s: credential expired at %s",
				srcAddr, expiry.Format(time.RFC3339))
			return nil, false
		}
		return turn.GenerateAuthKey(username, realm, ice.RESTCredential(secret, username)), true
	}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return def
}

package main

import (
	"net"
	"testing"
	"time"

	"github.com/pion/turn/v4"

	"drs/backend/internal/ice"
)

// TestAllocateWithProviderCredential is the one test that matters for the relay: it
// mints a credential exactly the way the backend hands it to a peer, then uses it to
// open a real allocation against a real running relay.
//
// This is checked rather than reasoned about because a credential-format mismatch is
// silent from both ends — the browser reports only that ICE failed and the relay only
// that a request was unauthorised — so it is not something inspection reliably catches.
func TestAllocateWithProviderCredential(t *testing.T) {
	const (
		secret = "test-shared-secret-not-a-real-one"
		realm  = "drs"
		port   = 13478
	)

	srv, err := start(config{
		PublicIP:   "127.0.0.1",
		ListenAddr: "127.0.0.1",
		Port:       port,
		Realm:      realm,
		Secret:     secret,
		MinPort:    49160,
		MaxPort:    49200,
	})
	if err != nil {
		t.Fatalf("start relay: %v", err)
	}
	defer srv.Close() //nolint:errcheck

	// Credentials come from the provider, not from the test, so the test cannot
	// accidentally agree with the relay while the real caller disagrees.
	servers := ice.NewProvider(ice.Config{
		TURNPublicIP: "127.0.0.1",
		TURNPort:     port,
		TURNSecret:   secret,
		TURNRealm:    realm,
	}).Servers()

	var username, credential string
	for _, s := range servers {
		if s.Username != "" {
			username, credential = s.Username, s.Credential
		}
	}
	if username == "" {
		t.Fatal("provider returned no TURN server with credentials")
	}

	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("client socket: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	client, err := turn.NewClient(&turn.ClientConfig{
		TURNServerAddr: net.JoinHostPort("127.0.0.1", "13478"),
		Username:       username,
		Password:       credential,
		Realm:          realm,
		Conn:           conn,
	})
	if err != nil {
		t.Fatalf("turn client: %v", err)
	}
	defer client.Close()

	if err := client.Listen(); err != nil {
		t.Fatalf("client listen: %v", err)
	}

	relayConn, err := client.Allocate()
	if err != nil {
		t.Fatalf("allocate with provider credential: %v", err)
	}
	defer relayConn.Close() //nolint:errcheck

	t.Logf("allocated relay address %s using username %q", relayConn.LocalAddr(), username)

	// An expired credential must be refused, or the TTL is decoration. The username is
	// the expiry, so backdating it is enough — and the HMAC is recomputed over the
	// backdated value so this tests expiry rather than a bad signature.
	stale := "1000000000"
	staleConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("client socket: %v", err)
	}
	defer staleConn.Close() //nolint:errcheck

	expiredClient, err := turn.NewClient(&turn.ClientConfig{
		TURNServerAddr: net.JoinHostPort("127.0.0.1", "13478"),
		Username:       stale,
		Password:       ice.RESTCredential(secret, stale),
		Realm:          realm,
		Conn:           staleConn,
		RTO:            500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("turn client: %v", err)
	}
	defer expiredClient.Close()

	if err := expiredClient.Listen(); err != nil {
		t.Fatalf("client listen: %v", err)
	}
	if _, err := expiredClient.Allocate(); err == nil {
		t.Fatal("an expired credential was accepted; the TTL is not enforced")
	}
}

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// Agent secrets are hashed with SHA-256 rather than bcrypt, which looks wrong at a
// glance and is not. bcrypt exists to make brute-forcing *low-entropy human-chosen*
// passwords expensive. GenerateSecret produces 32 bytes from crypto/rand, so there is
// no dictionary to attack and nothing for a work factor to buy. What we would pay for
// it is real: an agent re-authenticates on every reconnect, and a fleet of devices
// riding out a server restart would turn a bcrypt verify into a self-inflicted DoS.
//
// Passwords keep using bcrypt (see auth.go). Only full-entropy machine-generated
// secrets take this path.

// GenerateSecret returns nBytes of cryptographic randomness, hex-encoded.
func GenerateSecret(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// HashSecret returns the hex-encoded SHA-256 of a secret, for storage.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// SecretMatches reports whether submitted hashes to storedHash. The comparison is
// constant-time so a network attacker cannot recover the stored hash byte by byte by
// timing the responses.
func SecretMatches(storedHash, submitted string) bool {
	want, err := hex.DecodeString(storedHash)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	got := sha256.Sum256([]byte(submitted))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}

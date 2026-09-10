package config

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// The config trailer written by the backend's download endpoint, so an agent handed out
// through an invite link already knows where to enroll.
//
// This must stay byte-for-byte identical to backend/pkg/agentcfg — that package is the
// source of truth and carries the rationale for the layout. It is duplicated rather than
// imported because the agent is a separate Go module; the format is eleven bytes of
// framing and has a round-trip test on the backend side.
//
//	<base drs-agent.exe bytes>
//	<config JSON>              N bytes
//	<uint32 little-endian N>   4 bytes
//	"DRSCFG\x00\x01"           8 bytes
const (
	embeddedMagic      = "DRSCFG\x00\x01"
	embeddedFooterSize = 4 + len(embeddedMagic)
	embeddedMaxSize    = 64 << 10
)

// Embedded is the configuration baked into a downloaded agent.
type Embedded struct {
	ServerURL string `json:"serverUrl"`
	Token     string `json:"token"`
	Autostart bool   `json:"autostart"`
}

// ErrNoEmbedded means this binary carries no trailer — it was built locally or downloaded
// from the plain /downloads/ path rather than through an invite link. That is an ordinary
// state, not a failure: the caller falls back to asking the user.
var ErrNoEmbedded = errors.New("no embedded configuration")

// ReadEmbedded reads the configuration appended to this executable.
//
// Reading our own file while running is fine on Windows: the loader opens the image with
// FILE_SHARE_READ, so another read handle is permitted. It is deliberately the *file*
// that is read rather than the loaded image, because the trailer sits past the last
// section and is never mapped into memory.
func ReadEmbedded() (Embedded, error) {
	exe, err := os.Executable()
	if err != nil {
		return Embedded{}, err
	}
	return readEmbeddedFrom(exe)
}

func readEmbeddedFrom(path string) (Embedded, error) {
	f, err := os.Open(path)
	if err != nil {
		return Embedded{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return Embedded{}, err
	}
	total := info.Size()
	if total < int64(embeddedFooterSize) {
		return Embedded{}, ErrNoEmbedded
	}

	// Read the fixed footer first. A binary with no trailer — the common case for a
	// developer build — costs one 12-byte read and stops here.
	footer := make([]byte, embeddedFooterSize)
	if _, err := f.ReadAt(footer, total-int64(embeddedFooterSize)); err != nil {
		return Embedded{}, err
	}
	if string(footer[4:]) != embeddedMagic {
		return Embedded{}, ErrNoEmbedded
	}

	n := int64(binary.LittleEndian.Uint32(footer[:4]))
	if n <= 0 || n > embeddedMaxSize || n+int64(embeddedFooterSize) > total {
		// The magic matched but the length did not: the trailer is damaged, not absent.
		// Enrolling against a half-read URL is worse than asking the user, so this is an
		// error rather than a silent ErrNoEmbedded.
		return Embedded{}, fmt.Errorf("embedded configuration length %d is not plausible", n)
	}

	body := make([]byte, n)
	if _, err := f.ReadAt(body, total-int64(embeddedFooterSize)-n); err != nil && err != io.EOF {
		return Embedded{}, err
	}

	var e Embedded
	if err := json.Unmarshal(body, &e); err != nil {
		return Embedded{}, fmt.Errorf("embedded configuration is corrupt: %w", err)
	}
	if e.ServerURL == "" || e.Token == "" {
		return Embedded{}, errors.New("embedded configuration is missing the server URL or token")
	}
	return e, nil
}

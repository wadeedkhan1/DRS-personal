// Package agentcfg is the format of the configuration blob appended to a downloaded
// Windows agent, so a machine that runs it enrolls itself with nothing typed.
//
// It lives in pkg/ rather than internal/ for the same reason pkg/protocol does: the
// agent is a separate Go module that reads what the backend writes, and a wire format
// with two hand-written implementations drifts. The agent imports this package by copy
// (agents/windows/internal/config/embedded.go re-declares Read against these constants) —
// the constants and the layout comment here are the source of truth.
//
// # Why append rather than patch or rename
//
// A PE file's headers describe where its sections end; bytes after that are not mapped
// and the loader ignores them, so appending cannot corrupt the executable. The two
// alternatives are worse: rewriting the filename to carry the config breaks the moment
// anyone renames the file or a browser adds " (1)", and building a per-invite binary
// server-side would mean a Go toolchain and a CGO libvpx build in the container.
//
// The trade-off worth naming: this invalidates an Authenticode signature. The agent is
// not signed today, and if it ever is, the config has to move into a signed resource or
// a sidecar instead.
//
// # Layout
//
//	<base drs-agent.exe bytes>
//	<config JSON>              N bytes
//	<uint32 little-endian N>   4 bytes
//	<Magic>                    8 bytes
//
// The magic is last so a reader can seek to a fixed offset from the end, check 8 bytes,
// and give up immediately on a binary that has no trailer — the common case for a
// developer build, which must stay cheap and must never be misread as configured.
package agentcfg

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// Magic marks a configured binary. The NUL and version byte make an accidental
	// match against ordinary compiled data implausible, and give a way to change the
	// layout later without a reader guessing.
	Magic = "DRSCFG\x00\x01"

	// FooterSize is the fixed tail: 4-byte length + 8-byte magic.
	FooterSize = 4 + len(Magic)

	// MaxSize bounds what a reader will allocate from a length it found in a file. The
	// config is a URL and a token; anything approaching this is a corrupt or hostile
	// trailer, not a config.
	MaxSize = 64 << 10
)

// Config is what a downloaded agent needs in order to enroll without being told anything.
type Config struct {
	// ServerURL is the origin to enroll against, e.g. "https://drs.example.com".
	ServerURL string `json:"serverUrl"`
	// Token is the enrollment token the link was generated with.
	Token string `json:"token"`
	// Autostart asks the agent to register itself to run at login once enrolled.
	Autostart bool `json:"autostart"`
}

// Marshal renders the trailer to append to a base binary.
func Marshal(c Config) ([]byte, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	if len(body) > MaxSize {
		return nil, fmt.Errorf("agent config is %d bytes, over the %d limit", len(body), MaxSize)
	}
	out := make([]byte, 0, len(body)+FooterSize)
	out = append(out, body...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
	out = append(out, Magic...)
	return out, nil
}

// ErrNoConfig means the bytes carry no trailer. It is the ordinary answer for a binary
// built locally rather than downloaded, so callers treat it as "fall back to asking the
// user", not as a failure.
var ErrNoConfig = errors.New("no embedded agent config")

// Unmarshal reads a trailer from the tail of a binary. tail must be the last
// min(len(file), MaxSize+FooterSize) bytes of the file, and total is the file's full
// size — enough to validate a length without reading the whole executable into memory.
func Unmarshal(tail []byte, total int64) (Config, error) {
	if len(tail) < FooterSize {
		return Config{}, ErrNoConfig
	}
	footer := tail[len(tail)-FooterSize:]
	if string(footer[4:]) != Magic {
		return Config{}, ErrNoConfig
	}

	n := int64(binary.LittleEndian.Uint32(footer[:4]))
	// A length that does not fit in what precedes it means the trailer is damaged rather
	// than absent. Say so instead of silently enrolling against garbage.
	if n <= 0 || n > MaxSize || n+int64(FooterSize) > total || n > int64(len(tail)-FooterSize) {
		return Config{}, fmt.Errorf("embedded agent config length %d is not plausible", n)
	}

	body := tail[len(tail)-FooterSize-int(n) : len(tail)-FooterSize]
	var c Config
	if err := json.Unmarshal(body, &c); err != nil {
		return Config{}, fmt.Errorf("embedded agent config is corrupt: %w", err)
	}
	if c.ServerURL == "" || c.Token == "" {
		return Config{}, errors.New("embedded agent config is missing serverUrl or token")
	}
	return c, nil
}

// TailSize is how many bytes from the end of a file a reader should pass to Unmarshal.
func TailSize(total int64) int64 {
	max := int64(MaxSize + FooterSize)
	if total < max {
		return total
	}
	return max
}

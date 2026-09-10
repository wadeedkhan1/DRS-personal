package agentcfg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// appendTo mimics what the download handler does: base binary, then the trailer.
func appendTo(t *testing.T, base []byte, c Config) []byte {
	t.Helper()
	blob, err := Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return append(append([]byte{}, base...), blob...)
}

func readBack(t *testing.T, file []byte) (Config, error) {
	t.Helper()
	total := int64(len(file))
	return Unmarshal(file[total-TailSize(total):], total)
}

func TestRoundTrip(t *testing.T) {
	base := bytes.Repeat([]byte{0x4D, 0x5A, 0x90, 0x00}, 4096) // stand-in for a PE
	want := Config{ServerURL: "https://drs.example.com", Token: "DRS-ABC123", Autostart: true}

	got, err := readBack(t, appendTo(t, base, want))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != want {
		t.Errorf("round trip changed the config:\n got %+v\nwant %+v", got, want)
	}
}

// A locally built agent has no trailer, and must be cheap and unambiguous to reject —
// this is the common case, not an error case.
func TestPlainBinaryHasNoConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		file []byte
	}{
		{"empty", nil},
		{"shorter than the footer", []byte{1, 2, 3}},
		{"ordinary binary", bytes.Repeat([]byte{0xCC}, 8192)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readBack(t, tc.file); !errors.Is(err, ErrNoConfig) {
				t.Errorf("want ErrNoConfig, got %v", err)
			}
		})
	}
}

// Appending twice is what a double-personalised download would look like. The last
// trailer must win, so re-downloading cannot leave an agent pointed at a stale server.
func TestLastTrailerWins(t *testing.T) {
	base := bytes.Repeat([]byte{0xCC}, 1024)
	first := Config{ServerURL: "https://old.example.com", Token: "DRS-OLD"}
	second := Config{ServerURL: "https://new.example.com", Token: "DRS-NEW"}

	got, err := readBack(t, appendTo(t, appendTo(t, base, first), second))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ServerURL != second.ServerURL || got.Token != second.Token {
		t.Errorf("stale trailer won: got %+v, want %+v", got, second)
	}
}

// A damaged trailer must be an error, never a silent fall-through to "unconfigured" —
// enrolling against a garbage URL is worse than asking the user.
func TestCorruptTrailerIsAnError(t *testing.T) {
	base := bytes.Repeat([]byte{0xCC}, 1024)

	t.Run("length larger than the file", func(t *testing.T) {
		file := appendTo(t, base, Config{ServerURL: "https://x", Token: "DRS-1"})
		binary.LittleEndian.PutUint32(file[len(file)-FooterSize:], 1<<20)
		_, err := readBack(t, file)
		if err == nil || errors.Is(err, ErrNoConfig) {
			t.Errorf("want a plausibility error, got %v", err)
		}
	})

	t.Run("zero length", func(t *testing.T) {
		file := appendTo(t, base, Config{ServerURL: "https://x", Token: "DRS-1"})
		binary.LittleEndian.PutUint32(file[len(file)-FooterSize:], 0)
		if _, err := readBack(t, file); err == nil {
			t.Error("want an error for a zero-length config")
		}
	})

	t.Run("body is not json", func(t *testing.T) {
		body := []byte("not json at all")
		file := append(append([]byte{}, base...), body...)
		file = binary.LittleEndian.AppendUint32(file, uint32(len(body)))
		file = append(file, Magic...)
		_, err := readBack(t, file)
		if err == nil || !strings.Contains(err.Error(), "corrupt") {
			t.Errorf("want a corruption error, got %v", err)
		}
	})
}

// The server URL and token are the whole point; a trailer without them would enroll
// nowhere, so it must not be reported as a usable config.
func TestIncompleteConfigRejected(t *testing.T) {
	base := bytes.Repeat([]byte{0xCC}, 1024)
	for _, c := range []Config{
		{Token: "DRS-1"},
		{ServerURL: "https://x"},
	} {
		if _, err := readBack(t, appendTo(t, base, c)); err == nil {
			t.Errorf("want an error for %+v", c)
		}
	}
}

package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// machineIDFile sits beside the identity file. It is deliberately separate: the identity
// is discarded on re-enrollment, and the machine id must survive that — it is what says
// "this is the same physical machine", which is the whole reason it exists.
const machineIDFile = "machine-id"

// MachineID returns a stable identifier for this machine, creating one on first call.
//
// Devices used to be de-duplicated on hostname, which breaks the moment one invite link
// is rolled out across a fleet: cloned VMs and imaged corporate PCs routinely share a
// name, and the second machine to enroll would match the first one's row and rotate its
// secret — leaving the first agent reconnecting forever with a credential the server had
// already discarded. Silent, and very hard to diagnose from either end.
//
// A random UUID rather than a hardware serial or the Windows MachineGuid: reading those
// needs WMI or the registry, they are not reliably unique on cloned images either (a
// clone copies MachineGuid too), and a value we generate after the clone is made is
// exactly the property wanted. The cost is that wiping %AppData% makes a machine look
// new, which is the same thing that already loses the identity file.
//
// An unwritable config directory returns "", and the server falls back to hostname
// de-duplication — degraded, but no worse than before this existed.
func MachineID() string {
	path, err := machineIDPath()
	if err != nil {
		return ""
	}

	if raw, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id
		}
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	id := hex.EncodeToString(buf)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ""
	}
	// 0600 like the identity file. It is not a secret — it identifies rather than
	// authenticates — but it lives beside one and there is no reason to widen it.
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return ""
	}
	return id
}

func machineIDPath() (string, error) {
	identity, err := DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(identity), machineIDFile), nil
}

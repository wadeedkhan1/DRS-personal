package handlers

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"drs/backend/internal/auth"
	"drs/backend/internal/models"
	"drs/backend/pkg/agentcfg"
)

// Agent binaries staged for download. These filenames are the contract with
// deploy/downloads/ and with the nginx `location /downloads/` block, which keeps serving
// the same directory unmodified for anyone who wants the plain binary and the CLI flags.
var agentBinaries = map[string]string{
	models.DeviceTypeWindows: "drs-agent.exe",
	models.DeviceTypeAndroid: "drs-agent.apk",
}

// binaryPath resolves a platform to a staged file, or reports that this deployment has
// not published one.
func (h *Handler) binaryPath(platform string) (string, bool) {
	dir := h.cfg.AgentBinaryDir
	if dir == "" {
		return "", false
	}
	name, ok := agentBinaries[platform]
	if !ok {
		return "", false
	}
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	return path, true
}

// AgentAvailability reports which agents this deployment can hand out.
//
// Public, and deliberately says nothing about tokens: it answers "is there software to
// download here", which the enroll page needs before it knows whether to render a
// download button or an explanation.
//
// It replaces a HEAD probe against nginx's /downloads/, which was wrong in development:
// /downloads is not in the Vite proxy, so the probe hit the SPA fallback and reported
// every binary as present whether or not one existed.
func (h *Handler) AgentAvailability(w http.ResponseWriter, r *http.Request) {
	_, windows := h.binaryPath(models.DeviceTypeWindows)
	_, android := h.binaryPath(models.DeviceTypeAndroid)
	h.respondJSON(w, http.StatusOK, map[string]bool{
		"windows": windows,
		"android": android,
	})
}

// DownloadAgent serves an agent binary with this invite's configuration baked in, so the
// machine that runs it enrolls itself without anyone typing a server address or a token.
//
// Public for the same reason POST /api/devices/enroll is: the machine has no credential
// yet, and the token in the query string IS the credential. That is also the security
// weight of this endpoint — what it returns is a self-enrolling payload, so an unknown
// token must get nothing at all.
//
// Windows gets the config appended as a trailer (see pkg/agentcfg). Android cannot: the
// system renames an installed APK to base.apk and the app cannot read its own installer,
// so the APK is served unmodified and the phone is configured by the drs:// deep link on
// the enroll page instead.
func (h *Handler) DownloadAgent(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	if platform == "" {
		platform = models.DeviceTypeWindows
	}

	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// A token that cannot enroll must not yield a binary claiming it can — the machine
	// would install itself, fail, and leave someone debugging a silent agent. Same
	// clause as redeemToken, so a revoked link stops producing installers the moment it
	// stops producing devices.
	//
	// 404 rather than 403: an unknown token and a revoked one give the same answer, the
	// enumeration parity used across the rest of the API.
	var tokenID string
	err := h.db.QueryRow(`
		SELECT id FROM enrollment_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > NOW()
	`, auth.HashSecret(token)).Scan(&tokenID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	path, ok := h.binaryPath(platform)
	if !ok {
		http.Error(w, "no agent published for this platform", http.StatusNotFound)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "agent unavailable", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	filename := agentBinaries[platform]
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// A personalised binary is unique per invite and must never be cached by a proxy.
	w.Header().Set("Cache-Control", "no-store")

	if platform != models.DeviceTypeWindows {
		// Unmodified: Content-Length is known, so a browser can show real progress.
		if info, statErr := f.Stat(); statErr == nil {
			w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		}
		if _, err := io.Copy(w, f); err != nil && !isClientGone(err) {
			log.Printf("[DOWNLOAD WARN] streaming %s: %v", filename, err)
		}
		return
	}

	trailer, err := agentcfg.Marshal(agentcfg.Config{
		ServerURL: h.publicBaseURL(r),
		Token:     token,
		Autostart: true,
	})
	if err != nil {
		http.Error(w, "could not prepare the agent", http.StatusInternalServerError)
		return
	}

	// Length is known ahead of the write because the trailer is built first, which keeps
	// the download a normal progress-bar download rather than a chunked one of unknown
	// size — a binary that appears to hang is one people cancel halfway.
	if info, statErr := f.Stat(); statErr == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size()+int64(len(trailer)), 10))
	}

	if _, err := io.Copy(w, f); err != nil {
		if !isClientGone(err) {
			log.Printf("[DOWNLOAD WARN] streaming %s: %v", filename, err)
		}
		return
	}
	if _, err := w.Write(trailer); err != nil && !isClientGone(err) {
		// A truncated trailer is the one genuinely dangerous outcome: the agent would
		// read a damaged config rather than none. agentcfg.Unmarshal rejects that
		// explicitly rather than treating it as "unconfigured".
		log.Printf("[DOWNLOAD WARN] writing config trailer for %s: %v", filename, err)
	}
}

// isClientGone reports a download the browser abandoned. Common and uninteresting —
// someone cancelled or navigated away — so it is not worth a log line. Matched on the
// error text because the underlying types differ per platform and none of this is worth
// a build-tagged file.
func isClientGone(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "forcibly closed") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "aborted")
}

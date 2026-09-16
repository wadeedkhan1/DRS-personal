package config

import (
	"net/url"
	"strings"
)

// SplitInvite pulls a server base URL and (if present) a token out of whatever string was
// provided (e.g. pasted or typed). It accepts a full invite link
// (https://host:port/enroll?token=DRS-…), a bare server URL, or a host:port. ok is false
// when the input is empty.
func SplitInvite(s string) (server, token string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	// Give a scheme-less host:port a scheme so url.Parse treats it as a host, not a path.
	toParse := s
	if !strings.Contains(s, "://") {
		toParse = "http://" + s
	}
	if u, err := url.Parse(toParse); err == nil && u.Host != "" {
		scheme := u.Scheme
		if scheme == "" {
			scheme = "http"
		}
		server = scheme + "://" + u.Host
		token = u.Query().Get("token")
		return server, token, true
	}
	return strings.TrimRight(s, "/"), "", true
}

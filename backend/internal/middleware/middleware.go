package middleware

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"drs/backend/internal/auth"
)

type contextKey string

// UserContextKey is where validated claims live on the request context.
const UserContextKey contextKey = "user_claims"

// CORS answers preflights and sets the response headers for allowed origins.
//
// Origins are an explicit allowlist. The previous version reflected whatever Origin
// the caller sent while also setting Allow-Credentials: true, which is the one
// combination the spec forbids precisely because it hands every website on the
// internet authenticated access to the API.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	wildcard := slices.Contains(allowedOrigins, "*")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			switch {
			case wildcard:
				// Development convenience. Credentials stay off: with "*" the browser
				// would refuse the response anyway, and the portal authenticates with
				// a bearer token rather than cookies, so nothing needs them.
				w.Header().Set("Access-Control-Allow-Origin", "*")
			case origin != "" && originAllowed(origin, allowedOrigins):
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func originAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}

// Logger records method, path and duration.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("[HTTP] %s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

// Auth validates the bearer token on protected routes.
//
// The token is read from the Authorization header only. Accepting it from a query
// string, as this used to, writes credentials into nginx access logs, browser history
// and any Referer header the page emits. WebSocket routes, which cannot set a header,
// use the Sec-WebSocket-Protocol channel instead (see internal/ws).
func Auth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			parts := strings.Fields(authHeader)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				respondJSONError(w, http.StatusUnauthorized, "Missing authorization token")
				return
			}

			claims, err := auth.ValidateJWT(parts[1], jwtSecret)
			if err != nil {
				respondJSONError(w, http.StatusUnauthorized, "Invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), UserContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole gates a route on the caller's role.
func RequireRole(allowedRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := GetUserClaims(r)
			if !ok {
				respondJSONError(w, http.StatusUnauthorized, "Unauthorized")
				return
			}
			if !slices.Contains(allowedRoles, claims.Role) {
				respondJSONError(w, http.StatusForbidden, "Forbidden: insufficient permissions for this operation")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetUserClaims returns the validated claims attached by Auth.
func GetUserClaims(r *http.Request) (*auth.JWTClaims, bool) {
	claims, ok := r.Context().Value(UserContextKey).(*auth.JWTClaims)
	return claims, ok && claims != nil
}

func respondJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

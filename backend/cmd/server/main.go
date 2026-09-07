// Command server is the DRS backend: the REST API plus the WebRTC signaling relay.
//
// It is deliberately one process. Media never passes through it (SDS 4), so its load
// is signaling messages and API calls rather than video, and a single instance carries
// far more concurrent sessions than a relay would.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"drs/backend/internal/audit"
	"drs/backend/internal/config"
	"drs/backend/internal/database"
	"drs/backend/internal/handlers"
	"drs/backend/internal/ice"
	"drs/backend/internal/middleware"
	"drs/backend/internal/models"
	"drs/backend/internal/presence"
	"drs/backend/internal/ws"
)

func main() {
	log.Println("Starting DRS backend (REST API + WebRTC signaling relay)")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Presence and session tracking live in this process (SDS 6). Callers hold the
	// interfaces, not the concrete store, so a Redis-backed replacement is a change
	// here and nowhere else.
	presStore := presence.NewMemoryStore()
	auditLogger := audit.NewLogger(db.DB)

	iceProvider := ice.NewProvider(ice.Config{
		STUNURLs:     cfg.STUNURLs,
		TURNPublicIP: cfg.TURNPublicIP,
		TURNPort:     cfg.TURNPort,
		TURNSecret:   cfg.TURNSecret,
		TURNRealm:    cfg.TURNRealm,
		TURNCredTTL:  cfg.TURNCredTTL,
		ForceRelay:   cfg.ForceRelay,
	})
	if iceProvider.TransportPolicy() == "relay" {
		log.Printf("NAT traversal: ALL media forced through the TURN relay at %s "+
			"(no direct peer-to-peer)", cfg.TURNPublicIP)
	} else if iceProvider.TURNEnabled() {
		log.Printf("NAT traversal: STUN + TURN relay at %s (direct when possible)", cfg.TURNPublicIP)
	} else {
		// Worth saying out loud. Without a relay, sessions behind symmetric NAT or a
		// corporate firewall will fail to connect with no fallback (SDS 3.1), and that
		// describes a lot of the networks this product targets.
		log.Printf("NAT traversal: STUN only, no TURN relay configured — " +
			"sessions behind strict NAT/firewalls may fail to connect")
	}

	store := ws.NewSQLStore(db.DB)
	hub := ws.NewHub(ws.Options{
		Devices:           store,
		Sessions:          store,
		Presence:          presStore,
		Registry:          presStore,
		Audit:             auditLogger,
		ICE:               iceProvider,
		JWTSecret:         cfg.JWTSecret,
		AllowedOrigins:    cfg.AllowedOrigins,
		HeartbeatInterval: cfg.HeartbeatInterval,
		SessionFPS:        cfg.SessionFPS,
		SessionMaxWidth:   cfg.SessionMaxWidth,
	})

	h := handlers.NewHandler(cfg, db.DB, presStore, auditLogger, iceProvider)

	mux := http.NewServeMux()

	// Public. Enrollment has to be public because the agent has no credential yet; the
	// enrollment token is the credential, and it is single-use and expiring.
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("POST /api/auth/login", h.Login)
	mux.HandleFunc("POST /api/devices/enroll", h.EnrollDevice)

	// WebSocket endpoints authenticate themselves: the agent with a Hello frame, the
	// browser with a JWT in the Sec-WebSocket-Protocol header. Neither can use the
	// Authorization header, so neither goes through the Auth middleware.
	mux.HandleFunc("/ws/agent", hub.ServeAgent)
	mux.HandleFunc("/ws/session", hub.ServeSession)
	mux.HandleFunc("/ws/presence", hub.ServePresence)

	authMW := middleware.Auth(cfg.JWTSecret)
	superAdminOnly := middleware.RequireRole(models.RoleSuperAdmin)
	anyAdmin := middleware.RequireRole(models.RoleSuperAdmin, models.RoleAdmin)

	protected := func(role func(http.Handler) http.Handler, fn http.HandlerFunc) http.Handler {
		return authMW(role(fn))
	}

	// Readable by both roles, each scoped to what they may see.
	mux.Handle("GET /api/auth/me", protected(anyAdmin, h.Me))
	mux.Handle("GET /api/devices", protected(anyAdmin, h.ListDevices))
	mux.Handle("GET /api/devices/", protected(anyAdmin, h.GetDevice))
	mux.Handle("GET /api/groups", protected(anyAdmin, h.ListGroups))
	// Reading a team's members is scoped inside the handler the same way ListGroups is,
	// so an Admin can see who else is on their team without being able to change it.
	mux.Handle("GET /api/groups/", protected(anyAdmin, h.ListGroupMembers))
	mux.Handle("GET /api/sessions", protected(anyAdmin, h.ListSessions))
	mux.Handle("GET /api/audit-logs", protected(anyAdmin, h.ListAuditLogs))
	mux.Handle("GET /api/reports/usage", protected(anyAdmin, h.GetUsageReport))
	mux.Handle("GET /api/session/ice", protected(anyAdmin, h.ICEServers))

	// Super Admin only (SRS FR-1.5, FR-1.6).
	mux.Handle("POST /api/devices/enrollment-token", protected(superAdminOnly, h.GenerateEnrollmentToken))
	mux.Handle("PUT /api/devices/", protected(superAdminOnly, h.AssignDevice))
	mux.Handle("DELETE /api/devices/", protected(superAdminOnly, h.DeleteDevice))
	mux.Handle("POST /api/groups", protected(superAdminOnly, h.CreateGroup))
	mux.Handle("POST /api/groups/", protected(superAdminOnly, h.AddGroupMember))
	mux.Handle("PATCH /api/groups/", protected(superAdminOnly, h.UpdateGroup))
	// One DELETE handler per prefix is all the mux allows, so this one dispatches on the
	// path itself: /api/groups/{id} deletes a team, /api/groups/{id}/members/{userId}
	// removes a member.
	mux.Handle("DELETE /api/groups/", protected(superAdminOnly, h.DeleteGroupOrMember))
	mux.Handle("GET /api/users", protected(superAdminOnly, h.ListUsers))
	mux.Handle("POST /api/users", protected(superAdminOnly, h.CreateUser))
	mux.Handle("DELETE /api/users/", protected(superAdminOnly, h.DeleteUser))

	handler := middleware.CORS(cfg.AllowedOrigins)(middleware.Logger(mux))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%s", cfg.Port),
		Handler: handler,
		// No ReadTimeout or WriteTimeout: both would apply to hijacked WebSocket
		// connections and kill every long-lived socket on this server. Header reading is
		// bounded separately, and the socket layer sets its own per-message deadlines
		// (see internal/ws).
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("Listening on http://0.0.0.0:%s", cfg.Port)
		log.Printf("Signaling: /ws/agent (devices), /ws/session (viewers), /ws/presence (dashboard)")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Forced shutdown: %v", err)
	}
	log.Println("DRS backend stopped.")
}

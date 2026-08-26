// Package admin serves the Drip control panel: a JSON API plus an embedded UI,
// on its own listener so administrative traffic never shares a port with
// tunnel traffic.
package admin

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"drip/internal/server/auth"
	"drip/internal/server/store"
	"drip/internal/server/tunnel"

	"go.uber.org/zap"
)

// Config configures the admin server.
type Config struct {
	// Store is the control plane database. Required.
	Store *store.Store
	// Manager supplies the live tunnel list. Optional.
	Manager *tunnel.Manager
	// Authenticator has its credential cache invalidated when a client is
	// disabled, deleted, or rotated. Optional but strongly recommended.
	Authenticator *auth.Authenticator

	// Address is the listen address, e.g. "127.0.0.1:8444". Empty builds a
	// server with no listener of its own, for a deployment that only mounts
	// PublicHandler on another listener.
	Address string
	// PublicMount reports that Handler is also served on a public hostname,
	// which is what decides whether the session cookies a request gets carry
	// Secure. See secureCookieFor.
	PublicMount bool
	// TLSConfig serves the panel over TLS when set. A loopback bind ignores it:
	// see LoopbackAddress.
	TLSConfig *tls.Config
	// SessionTTL bounds a signed-in session. Zero uses DefaultSessionTTL.
	SessionTTL time.Duration

	// Deployment describes how clients reach this server, so the panel can show
	// the URL an allocation resolves to and the command a machine must run.
	Deployment Deployment

	Logger *zap.Logger
}

// Deployment is the public shape of this server.
type Deployment struct {
	// Domain is where clients connect, e.g. tunnel.example.com.
	Domain string
	// TunnelDomain is what tunnel URLs sit under. Tunnels resolve to
	// <subdomain>.<TunnelDomain>.
	TunnelDomain string
	// PublicPort is the port shown in URLs and connection commands.
	PublicPort int
	// TLS reports whether clients connect over TLS.
	TLS bool
}

// Server is the admin panel HTTP server.
type Server struct {
	store         *store.Store
	manager       *tunnel.Manager
	authenticator *auth.Authenticator

	httpServer    *http.Server
	listener      net.Listener
	sessionTTL    time.Duration
	deployment    Deployment
	secureCookies bool
	publicMount   bool
	// bootstrapped latches once an administrator exists, so the public mount
	// stops querying the database on every request. It never unlatches:
	// administrators cannot be deleted through the API.
	bootstrapped atomic.Bool
	limiter      *loginLimiter
	logger       *zap.Logger

	stopCh chan struct{}
}

// New builds an admin server.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("the admin panel requires the control plane database")
	}
	if cfg.Address == "" && !cfg.PublicMount {
		return nil, fmt.Errorf("admin listen address is required unless the panel is mounted publicly")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	ttl := cfg.SessionTTL
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}

	// The server's certificate is issued for the tunnel domain, so presenting it
	// on 127.0.0.1 would fail name verification in every browser, every time.
	// A loopback panel is reached through SSH or a VPN, which already provides
	// the transport security TLS would add here.
	tlsConfig := cfg.TLSConfig
	if tlsConfig != nil && LoopbackAddress(cfg.Address) {
		logger.Info("Admin panel bound to loopback; serving plain HTTP",
			zap.String("address", cfg.Address),
			zap.String("reason", "the server certificate does not cover a loopback address"),
		)
		tlsConfig = nil
	}

	s := &Server{
		store:         cfg.Store,
		manager:       cfg.Manager,
		authenticator: cfg.Authenticator,
		sessionTTL:    ttl,
		deployment:    cfg.Deployment,
		secureCookies: tlsConfig != nil,
		publicMount:   cfg.PublicMount,
		limiter:       newLoginLimiter(10, 15*time.Minute),
		logger:        logger,
		stopCh:        make(chan struct{}),
	}

	if cfg.Address == "" {
		return s, nil
	}

	s.httpServer = &http.Server{
		Addr:              cfg.Address,
		Handler:           s.Handler(),
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}

	return s, nil
}

// LoopbackAddress reports whether addr binds only to the loopback interface.
func LoopbackAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Handler builds the routing tree. Exported so tests can drive it directly.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Unauthenticated: first-run setup and sign-in.
	mux.HandleFunc("GET /api/bootstrap", s.handleBootstrapStatus)
	mux.HandleFunc("POST /api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /api/session", s.handleLogin)
	mux.HandleFunc("GET /api/health", s.handleHealth)

	mux.Handle("GET /api/server", s.authed(s.handleServerInfo))

	// Authenticated.
	mux.Handle("GET /api/session", s.authed(s.handleWhoami))
	mux.Handle("DELETE /api/session", s.authed(s.handleLogout))

	mux.Handle("GET /api/accounts", s.authed(s.handleListAccounts))
	mux.Handle("POST /api/accounts", s.adminOnly(s.handleCreateAccount))
	mux.Handle("PATCH /api/accounts/{id}", s.adminOnly(s.handleUpdateAccount))
	mux.Handle("DELETE /api/accounts/{id}", s.adminOnly(s.handleDeleteAccount))

	mux.Handle("GET /api/clients", s.authed(s.handleListClients))
	mux.Handle("POST /api/clients", s.adminOnly(s.handleCreateClient))
	mux.Handle("PATCH /api/clients/{id}", s.adminOnly(s.handleUpdateClient))
	mux.Handle("DELETE /api/clients/{id}", s.adminOnly(s.handleDeleteClient))
	mux.Handle("POST /api/clients/{id}/rotate", s.adminOnly(s.handleRotateClient))

	mux.Handle("GET /api/reservations", s.authed(s.handleListReservations))
	mux.Handle("POST /api/reservations", s.adminOnly(s.handleCreateReservation))
	mux.Handle("PATCH /api/reservations/{id}", s.adminOnly(s.handleUpdateReservation))
	mux.Handle("DELETE /api/reservations/{id}", s.adminOnly(s.handleDeleteReservation))

	mux.Handle("GET /api/tunnels", s.authed(s.handleListTunnels))
	mux.Handle("GET /api/sessions", s.authed(s.handleListSessions))
	mux.Handle("POST /api/sessions/{id}/pin", s.adminOnly(s.handlePinSession))
	mux.Handle("POST /api/provision", s.adminOnly(s.handleProvision))
	mux.Handle("GET /api/audit", s.authed(s.handleListAudit))

	// The embedded UI serves everything else.
	mux.Handle("/", s.uiHandler())

	return securityHeaders(mux)
}

// PublicHandler wraps Handler for a mount on a public hostname.
//
// First-run setup is unauthenticated by necessity, so it is never reachable
// from the public internet: the wrapper refuses POST /api/bootstrap outright,
// and refuses everything while the deployment still has no administrator. Both
// checks fail closed — a database error reads as "not set up yet" — so the
// only way to bootstrap a deployment stays the panel's own listener, which is
// bound to loopback by default.
func (s *Server) PublicHandler() http.Handler {
	inner := s.Handler()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hasAdministrator(r.Context()) {
			writeError(w, http.StatusServiceUnavailable,
				"this panel has not been set up yet; complete first-run setup on the server's own admin address")
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/bootstrap" {
			writeError(w, http.StatusForbidden,
				"first-run setup is only available on the server's own admin address")
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// hasAdministrator reports whether the deployment has been bootstrapped. The
// answer latches, so the database is read only until the first administrator
// shows up.
func (s *Server) hasAdministrator(ctx context.Context) bool {
	if s.bootstrapped.Load() {
		return true
	}
	count, err := s.store.CountAdminUsers(ctx)
	if err != nil {
		s.logger.Error("Failed to count admin users", zap.Error(err))
		return false
	}
	if count == 0 {
		return false
	}
	s.bootstrapped.Store(true)
	return true
}

// LoopbackHost reports whether a request's Host header names the loopback
// interface, port included or not.
func LoopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Start begins serving on the panel's own listener. It returns once the
// listener is bound, and does nothing when the panel was built without an
// address of its own.
func (s *Server) Start() error {
	if s.httpServer == nil {
		go s.purgeLoop()
		return nil
	}

	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.httpServer.Addr, err)
	}
	if s.httpServer.TLSConfig != nil {
		ln = tls.NewListener(ln, s.httpServer.TLSConfig)
	}
	s.listener = ln

	go s.purgeLoop()

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("Admin server error", zap.Error(err))
		}
	}()

	s.logger.Info("Admin panel started",
		zap.String("address", ln.Addr().String()),
		zap.Bool("tls", s.httpServer.TLSConfig != nil),
	)
	return nil
}

// Addr returns the bound address, or nil before Start.
func (s *Server) Addr() net.Addr {
	if s == nil || s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Stop shuts the server down gracefully.
func (s *Server) Stop(ctx context.Context) error {
	select {
	case <-s.stopCh:
		return nil
	default:
		close(s.stopCh)
	}
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// purgeLoop periodically drops expired sessions and stale login counters.
func (s *Server) purgeLoop() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if _, err := s.store.PurgeExpiredAdminSessions(ctx); err != nil {
				s.logger.Warn("Failed to purge expired sessions", zap.Error(err))
			}
			cancel()
			s.limiter.purge()
		case <-s.stopCh:
			return
		}
	}
}

// contextKey namespaces values this package stores on a request context.
type contextKey string

const userContextKey contextKey = "admin-user"

// userFrom returns the authenticated operator attached by the auth middleware.
func userFrom(r *http.Request) *store.AdminUser {
	user, _ := r.Context().Value(userContextKey).(*store.AdminUser)
	return user
}

// authed requires a valid session. Read-only for viewers, mutating verbs are
// additionally gated by adminOnly.
func (s *Server) authed(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, err := s.currentUser(r)
		if err != nil {
			if !errors.Is(err, errNoSession) {
				s.logger.Error("Session lookup failed", zap.Error(err))
			}
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}

		// Any verb that changes state must carry the CSRF token.
		if isMutating(r.Method) {
			if err := checkCSRF(r); err != nil {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next(w, r.WithContext(ctx))
	})
}

// adminOnly requires a valid session whose role may change things.
func (s *Server) adminOnly(next http.HandlerFunc) http.Handler {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		user := userFrom(r)
		if user == nil || user.Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, "this action requires the admin role")
			return
		}
		next(w, r)
	})
}

func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// securityHeaders applies a restrictive baseline to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// The UI is fully self-contained, so nothing needs to load off-origin.
		h.Set("Content-Security-Policy",
			"default-src 'none'; style-src 'self' 'unsafe-inline'; script-src 'self'; "+
				"img-src 'self' data:; connect-src 'self'; form-action 'none'; "+
				"frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// writeJSON sends a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent; nothing useful is left to do.
		return
	}
}

// writeError sends a JSON error body.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// readJSON decodes a request body, rejecting unknown fields so a typo in the
// UI surfaces as an error instead of being silently ignored.
func readJSON(r *http.Request, dst interface{}) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

// storeStatus maps a store error onto an HTTP status.
func storeStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

// audit records an admin action, logging rather than failing if it cannot.
func (s *Server) audit(r *http.Request, action, targetType, targetID, detail string) {
	user := userFrom(r)
	actorID := ""
	if user != nil {
		actorID = user.Username
	}

	entry := &store.AuditEntry{
		ActorType:  store.ActorAdmin,
		ActorID:    actorID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Detail:     detail,
		IP:         clientIP(r),
	}

	if err := s.store.AppendAudit(r.Context(), entry); err != nil {
		s.logger.Warn("Failed to write audit entry",
			zap.String("action", action),
			zap.Error(err),
		)
	}
}

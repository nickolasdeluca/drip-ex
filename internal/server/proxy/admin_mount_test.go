package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drip/internal/server/tunnel"

	"go.uber.org/zap"
)

func newAdminMountHandler(t *testing.T, adminHandler http.Handler) *Handler {
	t.Helper()

	return NewHandler(HandlerConfig{
		Manager:      tunnel.NewManager(zap.NewNop()),
		Logger:       zap.NewNop(),
		ServerDomain: "tunnel.example.com",
		TunnelDomain: "tunnel.example.com",
		AdminHandler: adminHandler,
	})
}

// The panel takes over the server domain, and only the server domain: a
// request for a tunnel subdomain must never be answered by the control plane.
func TestAdminHandlerOwnsServerDomainOnly(t *testing.T) {
	admin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := newAdminMountHandler(t, admin)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "tunnel.example.com"
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("server domain: got %d, want the admin handler's %d", rec.Code, http.StatusTeapot)
	}

	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "abc123.tunnel.example.com"
	h.ServeHTTP(rec, r)
	if rec.Code == http.StatusTeapot {
		t.Fatal("a tunnel subdomain reached the admin handler")
	}
}

// The install command has to stay reachable without an account, so the landing
// page keeps a path of its own once the panel owns the root.
func TestInstallPageSurvivesAdminMount(t *testing.T) {
	admin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := newAdminMountHandler(t, admin)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, installPagePath, nil)
	r.Host = "tunnel.example.com"
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("%s: got %d, want %d", installPagePath, rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "install.sh") {
		t.Fatalf("%s did not serve the landing page", installPagePath)
	}
}

// Without a panel to mount, the server domain keeps serving the landing page.
func TestServerDomainKeepsLandingPageWithoutAdmin(t *testing.T) {
	h := newAdminMountHandler(t, nil)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "tunnel.example.com"
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("landing page: got %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "install.sh") {
		t.Fatal("server domain did not serve the landing page")
	}
}

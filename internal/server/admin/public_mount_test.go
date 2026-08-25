package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"drip/internal/server/auth"
	"drip/internal/server/store"
)

func newPublicTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	s, err := New(Config{
		Store:       st,
		Address:     "",
		PublicMount: true,
	})
	if err != nil {
		t.Fatalf("new admin server: %v", err)
	}
	return s, st
}

func addAdmin(t *testing.T, st *store.Store) {
	t.Helper()

	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := st.CreateAdminUser(context.Background(), &store.AdminUser{
		Username:     "root",
		PasswordHash: hash,
		Role:         store.RoleAdmin,
		Enabled:      true,
	}); err != nil {
		t.Fatalf("create admin user: %v", err)
	}
}

// The public mount must stay shut until the deployment has an administrator,
// or publishing the panel would publish the unauthenticated setup screen.
func TestPublicHandlerClosedBeforeBootstrap(t *testing.T) {
	s, st := newPublicTestServer(t)
	h := s.PublicHandler()

	for _, path := range []string{"/", "/api/session", "/api/bootstrap"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s before bootstrap: got %d, want %d", path, rec.Code, http.StatusServiceUnavailable)
		}
	}

	addAdmin(t, st)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("panel after bootstrap: got %d, want %d", rec.Code, http.StatusOK)
	}
}

// Bootstrap stays off the public mount for good: it is the one endpoint that
// cannot demand a session.
func TestPublicHandlerRefusesBootstrap(t *testing.T) {
	s, st := newPublicTestServer(t)
	addAdmin(t, st)

	rec := httptest.NewRecorder()
	s.PublicHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/bootstrap", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("public bootstrap: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// The same server answers a loopback listener and a public hostname, so the
// Secure attribute has to be decided per request rather than per server.
func TestSecureCookieFor(t *testing.T) {
	cases := []struct {
		name          string
		secureCookies bool
		publicMount   bool
		host          string
		want          bool
	}{
		{"loopback panel over plain http", false, false, "127.0.0.1:8444", false},
		{"loopback panel while published", false, true, "127.0.0.1:8444", false},
		{"localhost while published", false, true, "localhost:8444", false},
		{"public host while published", false, true, "tunnel.example.com", true},
		{"own tls listener", true, false, "tunnel.example.com", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{secureCookies: tc.secureCookies, publicMount: tc.publicMount}
			r := httptest.NewRequest(http.MethodPost, "/api/session", nil)
			r.Host = tc.host
			r.TLS = nil

			if got := s.secureCookieFor(r); got != tc.want {
				t.Fatalf("secureCookieFor(%q): got %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

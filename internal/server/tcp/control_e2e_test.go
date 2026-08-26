package tcp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	clienttcp "drip/internal/client/tcp"
	"drip/internal/server/auth"
	"drip/internal/server/reservations"
	"drip/internal/server/store"
	"drip/internal/server/tunnel"
	"drip/internal/shared/protocol"
)

// selfSignedTLSConfig builds a throwaway server TLS config. The client dials
// with verification off, so the name on the certificate does not matter.
func selfSignedTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "drip.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"drip.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	}
}

// newTLSTestServer starts a listener a real client can connect to.
func newTLSTestServer(t *testing.T) *authTestServer {
	t.Helper()

	logger := zap.NewNop()

	st, err := store.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	authenticator := auth.New(auth.Config{Store: st, AllowAnonymous: false, Logger: logger})
	t.Cleanup(authenticator.Close)

	manager := tunnel.NewManager(logger)
	t.Cleanup(manager.Shutdown)

	portAlloc, err := NewPortAllocator(38100, 38150)
	if err != nil {
		t.Fatalf("NewPortAllocator() error = %v", err)
	}

	listener := NewListener(ListenerConfig{
		Address:       "127.0.0.1:0",
		TLSConfig:     selfSignedTLSConfig(t),
		Authenticator: authenticator,
		Resolver:      reservations.New(st, false, logger),
		Sessions:      st,
		Manager:       manager,
		Logger:        logger,
		PortAlloc:     portAlloc,
		Domain:        "local.test",
		TunnelDomain:  "local.test",
		PublicPort:    443,
	})
	if err := listener.Start(); err != nil {
		t.Fatalf("listener.Start() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Stop() })

	return &authTestServer{
		listener: listener,
		manager:  manager,
		store:    st,
		addr:     listener.Addr().String(),
	}
}

// waitFor polls until cond holds, so a test never races the handshake.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The claim flow's last mile: an operator pins a name and the server moves the
// client onto it, with nobody touching the machine the client runs on.
func TestClientRebindsOnServerRequest(t *testing.T) {
	ts := newTLSTestServer(t)
	token, _, _ := ts.seedAccountCredential(t, "acme", 0)

	client := clienttcp.NewPoolClient(&clienttcp.ConnectorConfig{
		ServerAddr: ts.addr,
		Token:      token,
		TunnelType: protocol.TunnelTypeHTTP,
		LocalHost:  "127.0.0.1",
		LocalPort:  9765,
		Transport:  clienttcp.TransportTCP,
		Insecure:   true,
	}, zap.NewNop())
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	subdomain := client.GetSubdomain()
	if subdomain == "" {
		t.Fatal("GetSubdomain() is empty after a successful connect")
	}

	var conn *tunnel.Connection
	waitFor(t, "the tunnel to register", func() bool {
		c, ok := ts.manager.Get(subdomain)
		conn = c
		return ok && c != nil
	})

	// The client opens the control stream on its own, right after the session
	// comes up.
	waitFor(t, "the control stream", conn.HasControlStream)

	if err := conn.Rebind("billing", "pinned from the admin panel"); err != nil {
		t.Fatalf("Rebind() error = %v", err)
	}

	waitFor(t, "the client to take the rebind", func() bool {
		name, ok := client.PendingRebind()
		return ok && name == "billing"
	})

	// Acting on a rebind means registering again, so the client drops the
	// session it is on.
	waitFor(t, "the client to drop its session", client.IsClosed)
}

package tcp

import (
	"net"
	"testing"
	"time"

	json "github.com/goccy/go-json"

	"drip/internal/shared/protocol"
)

// waitForSessions polls until the control plane holds the expected number of
// live sessions. Teardown is asynchronous: the client's close travels back
// through the listener before the row goes away.
func waitForSessions(t *testing.T, ts *authTestServer, want int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		list, err := ts.store.ListSessions(t.Context(), "")
		if err != nil {
			t.Fatalf("ListSessions() error = %v", err)
		}
		if len(list) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ListSessions() returned %d sessions, want %d", len(list), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A registration is what the panel shows and what the pin endpoint acts on, so
// it has to leave a row behind.
func TestRegistrationRecordsLiveSession(t *testing.T) {
	ts := newTestServer(t, true, false)
	token, acct, client := ts.seedAccountCredential(t, "acme", 0)

	resp := registerOK(t, ts, protocol.RegisterRequest{
		Token:      token,
		TunnelType: protocol.TunnelTypeHTTP,
		LocalPort:  9765,
	})

	waitForSessions(t, ts, 1)

	list, err := ts.store.ListSessions(t.Context(), "")
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}

	sess := list[0]
	if sess.Subdomain != resp.Subdomain {
		t.Errorf("Subdomain = %q, want %q", sess.Subdomain, resp.Subdomain)
	}
	if sess.AccountID != acct.ID || sess.ClientID != client.ID {
		t.Errorf("session = %+v, want it owned by %s/%s", sess, acct.ID, client.ID)
	}
	if sess.LocalPort != 9765 {
		t.Errorf("LocalPort = %d, want 9765", sess.LocalPort)
	}
	if sess.ReservationID != nil {
		t.Errorf("ReservationID = %v, want nil for an ephemeral tunnel", *sess.ReservationID)
	}
}

// The row must die with the tunnel, or the panel would offer to pin something
// that is long gone.
func TestSessionClearedWhenTunnelEnds(t *testing.T) {
	ts := newTestServer(t, true, false)
	token := ts.seedCredential(t, 0)

	conn, err := net.DialTimeout("tcp", ts.addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}

	payload, err := json.Marshal(protocol.RegisterRequest{
		Token:      token,
		TunnelType: protocol.TunnelTypeHTTP,
		LocalPort:  9765,
	})
	if err != nil {
		t.Fatalf("marshal error = %v", err)
	}
	if err := protocol.WriteFrame(conn, protocol.NewFrame(protocol.FrameTypeRegister, payload)); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	if _, err := protocol.ReadFrame(conn); err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}

	waitForSessions(t, ts, 1)

	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	waitForSessions(t, ts, 0)
}

package admin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"drip/internal/server/store"
	"drip/internal/server/tunnel"
	"drip/internal/shared/protocol"
	"drip/internal/shared/utils"

	"go.uber.org/zap"
)

func newPinTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	s, err := New(Config{Store: st, Address: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("new admin server: %v", err)
	}
	return s, st
}

// seedLiveTunnel records an account, a credential and the session that a
// registration would have written for them.
func seedLiveTunnel(t *testing.T, st *store.Store, subdomain string) (*store.Account, *store.Client, *store.Session) {
	t.Helper()
	ctx := context.Background()

	acct, err := st.CreateAccount(ctx, "acme", 0)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	client := &store.Client{
		ID:         utils.GenerateShortID() + utils.GenerateShortID(),
		AccountID:  acct.ID,
		Name:       "windows-box",
		SecretHash: "hash",
		Enabled:    true,
	}
	if err := st.CreateClient(ctx, client); err != nil {
		t.Fatalf("create client: %v", err)
	}

	sess := &store.Session{
		AccountID:  acct.ID,
		ClientID:   client.ID,
		TunnelType: store.TunnelTypeHTTP,
		Subdomain:  subdomain,
		LocalPort:  9765,
	}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return acct, client, sess
}

func pinRequest(t *testing.T, sessionID, body string) *http.Request {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/pin", reader)
	req.SetPathValue("id", sessionID)
	return req
}

func TestPinSessionKeepsTheLiveName(t *testing.T) {
	s, st := newPinTestServer(t)
	acct, client, sess := seedLiveTunnel(t, st, "wild-otter")

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, sess.ID, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		ID        string  `json:"id"`
		AccountID string  `json:"account_id"`
		ClientID  *string `json:"client_id"`
		Subdomain string  `json:"subdomain"`
		Enabled   bool    `json:"enabled"`
		Renamed   bool    `json:"renamed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if out.Subdomain != "wild-otter" || out.Renamed {
		t.Fatalf("pinned %+v, want wild-otter unrenamed", out)
	}
	if out.AccountID != acct.ID || out.ClientID == nil || *out.ClientID != client.ID {
		t.Fatalf("pinned %+v, want it bound to %s/%s", out, acct.ID, client.ID)
	}
	if !out.Enabled {
		t.Error("pinned reservation is disabled, want enabled")
	}

	// Pinning under the live name describes the tunnel that is up right now,
	// so the session points at the reservation.
	got, err := st.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if got.ReservationID == nil || *got.ReservationID != out.ID {
		t.Fatalf("session reservation = %v, want %s", got.ReservationID, out.ID)
	}
}

// Rename-on-pin reserves a different name. The live tunnel keeps the one it
// registered with, so the session must not claim to hold the new reservation.
func TestPinSessionRenames(t *testing.T) {
	s, st := newPinTestServer(t)
	_, _, sess := seedLiveTunnel(t, st, "wild-otter")

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, sess.ID, `{"subdomain":"Billing"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Subdomain string `json:"subdomain"`
		Renamed   bool   `json:"renamed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if out.Subdomain != "billing" || !out.Renamed {
		t.Fatalf("pinned %+v, want billing marked as renamed", out)
	}

	got, err := st.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if got.ReservationID != nil {
		t.Fatalf("session reservation = %v, want nil after a rename", *got.ReservationID)
	}
}

// A reservation binds a credential. A tunnel that registered anonymously or on
// the legacy shared token has none, and inventing an owner would hand the name
// to a client that cannot prove it.
func TestPinSessionRefusesUnauthenticatedTunnel(t *testing.T) {
	s, st := newPinTestServer(t)

	sess := &store.Session{TunnelType: store.TunnelTypeHTTP, Subdomain: "loose"}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, sess.ID, ""))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "client credential") {
		t.Errorf("body = %s, want it to name the missing credential", rec.Body.String())
	}
}

func TestPinSessionRefusesAlreadyBound(t *testing.T) {
	s, st := newPinTestServer(t)
	ctx := context.Background()
	acct, _, sess := seedLiveTunnel(t, st, "wild-otter")

	res := &store.Reservation{
		AccountID:  acct.ID,
		TunnelType: store.TunnelTypeHTTP,
		Subdomain:  "wild-otter",
		Enabled:    true,
	}
	if err := st.CreateReservation(ctx, res); err != nil {
		t.Fatalf("create reservation: %v", err)
	}
	if err := st.SetSessionReservation(ctx, sess.ID, res.ID); err != nil {
		t.Fatalf("link session: %v", err)
	}

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, sess.ID, ""))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// Pinning a name somebody else already reserved is a conflict, not a silent
// second owner.
func TestPinSessionRefusesTakenName(t *testing.T) {
	s, st := newPinTestServer(t)
	ctx := context.Background()
	acct, _, sess := seedLiveTunnel(t, st, "wild-otter")

	taken := &store.Reservation{
		AccountID:  acct.ID,
		TunnelType: store.TunnelTypeHTTP,
		Subdomain:  "billing",
		Enabled:    true,
	}
	if err := st.CreateReservation(ctx, taken); err != nil {
		t.Fatalf("create reservation: %v", err)
	}

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, sess.ID, `{"subdomain":"billing"}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestPinTCPSessionReservesItsPort(t *testing.T) {
	s, st := newPinTestServer(t)
	ctx := context.Background()

	acct, client, _ := seedLiveTunnel(t, st, "wild-otter")
	tcpSess := &store.Session{
		AccountID:  acct.ID,
		ClientID:   client.ID,
		TunnelType: store.TunnelTypeTCP,
		Subdomain:  "tcp-20050",
		TCPPort:    20050,
	}
	if err := st.CreateSession(ctx, tcpSess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, tcpSess.ID, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		TunnelType string `json:"tunnel_type"`
		TCPPort    int    `json:"tcp_port"`
		Subdomain  string `json:"subdomain"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if out.TunnelType != store.TunnelTypeTCP || out.TCPPort != 20050 || out.Subdomain != "" {
		t.Fatalf("pinned %+v, want tcp port 20050 with no name", out)
	}

}

// A port is not a name: asking to rename one is a mistake worth reporting.
func TestPinTCPSessionRejectsAName(t *testing.T) {
	s, st := newPinTestServer(t)
	ctx := context.Background()

	acct, client, _ := seedLiveTunnel(t, st, "wild-otter")
	tcpSess := &store.Session{
		AccountID:  acct.ID,
		ClientID:   client.ID,
		TunnelType: store.TunnelTypeTCP,
		Subdomain:  "tcp-20051",
		TCPPort:    20051,
	}
	if err := st.CreateSession(ctx, tcpSess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, tcpSess.ID, `{"subdomain":"billing"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestPinSessionRejectsUnknownSession(t *testing.T) {
	s, _ := newPinTestServer(t)

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, "does-not-exist", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestListSessionsReturnsRecordedTunnels(t *testing.T) {
	s, st := newPinTestServer(t)
	_, client, sess := seedLiveTunnel(t, st, "wild-otter")

	rec := httptest.NewRecorder()
	s.handleListSessions(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var out []struct {
		ID        string `json:"id"`
		ClientID  string `json:"client_id"`
		Subdomain string `json:"subdomain"`
		LocalPort int    `json:"local_port"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("listed %d sessions, want 1", len(out))
	}
	if out[0].ID != sess.ID || out[0].ClientID != client.ID || out[0].LocalPort != 9765 {
		t.Fatalf("session = %+v, want the recorded one", out[0])
	}
}

// liveTunnel registers a tunnel in the manager so the pin handler can find it,
// and hands back the peer end of its control stream.
func liveTunnel(t *testing.T, s *Server, subdomain string, withControl bool) net.Conn {
	t.Helper()

	manager := tunnel.NewManager(zap.NewNop())
	t.Cleanup(manager.Shutdown)
	s.manager = manager

	if _, err := manager.RegisterOwned(nil, subdomain, "", tunnel.Owner{}); err != nil {
		t.Fatalf("RegisterOwned() error = %v", err)
	}
	conn, ok := manager.Get(subdomain)
	if !ok {
		t.Fatalf("Get(%q) found nothing right after registering it", subdomain)
	}
	if !withControl {
		return nil
	}

	server, client := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	conn.SetControlStream(server)
	return client
}

// The whole point of the rebind: an operator pins a name and the client moves
// without anyone touching the machine it runs on.
func TestPinSessionRebindsTheLiveClient(t *testing.T) {
	s, st := newPinTestServer(t)
	_, _, sess := seedLiveTunnel(t, st, "wild-otter")
	peer := liveTunnel(t, s, "wild-otter", true)

	frames := make(chan *protocol.Frame, 1)
	go func() {
		_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
		frame, err := protocol.ReadFrame(peer)
		if err != nil {
			close(frames)
			return
		}
		frames <- frame
	}()

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, sess.ID, `{"subdomain":"billing"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Renamed bool `json:"renamed"`
		Rebound bool `json:"rebound"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !out.Rebound || !out.Renamed {
		t.Fatalf("response = %+v, want renamed and rebound", out)
	}

	frame, ok := <-frames
	if !ok {
		t.Fatal("no rebind frame reached the client")
	}
	defer frame.Release()

	if frame.Type != protocol.FrameTypeRebind {
		t.Fatalf("frame type = %s, want Rebind", frame.Type)
	}
	var req protocol.RebindRequest
	if err := json.Unmarshal(frame.Payload, &req); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if req.Subdomain != "billing" {
		t.Fatalf("rebind subdomain = %q, want billing", req.Subdomain)
	}
}

// A client too old to open a control stream cannot be moved. The allocation is
// still correct, and the response says the move has not happened yet.
func TestPinSessionWithoutControlStreamStillPins(t *testing.T) {
	s, st := newPinTestServer(t)
	_, _, sess := seedLiveTunnel(t, st, "wild-otter")
	liveTunnel(t, s, "wild-otter", false)

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, sess.ID, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Subdomain string `json:"subdomain"`
		Rebound   bool   `json:"rebound"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if out.Subdomain != "wild-otter" {
		t.Errorf("Subdomain = %q, want wild-otter", out.Subdomain)
	}
	if out.Rebound {
		t.Error("Rebound = true, want false without a control stream")
	}
}

// A TCP client asks for a port by name, so that is what the rebind carries.
func TestPinTCPSessionRebindsWithThePortName(t *testing.T) {
	s, st := newPinTestServer(t)
	ctx := context.Background()
	acct, client, _ := seedLiveTunnel(t, st, "wild-otter")

	tcpSess := &store.Session{
		AccountID:  acct.ID,
		ClientID:   client.ID,
		TunnelType: store.TunnelTypeTCP,
		Subdomain:  "tcp-20050",
		TCPPort:    20050,
	}
	if err := st.CreateSession(ctx, tcpSess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	peer := liveTunnel(t, s, "tcp-20050", true)

	frames := make(chan *protocol.Frame, 1)
	go func() {
		_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
		frame, err := protocol.ReadFrame(peer)
		if err != nil {
			close(frames)
			return
		}
		frames <- frame
	}()

	rec := httptest.NewRecorder()
	s.handlePinSession(rec, pinRequest(t, tcpSess.ID, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	frame, ok := <-frames
	if !ok {
		t.Fatal("no rebind frame reached the client")
	}
	defer frame.Release()

	var req protocol.RebindRequest
	if err := json.Unmarshal(frame.Payload, &req); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if req.Subdomain != "tcp-20050" {
		t.Fatalf("rebind subdomain = %q, want tcp-20050", req.Subdomain)
	}
}

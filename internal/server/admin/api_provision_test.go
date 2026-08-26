package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"drip/internal/server/store"
	"drip/internal/shared/utils"
)

func newProvisionTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	s, err := New(Config{
		Store:   st,
		Address: "127.0.0.1:0",
		Deployment: Deployment{
			Domain:       "tunnel.example.com",
			TunnelDomain: "tunnel.example.com",
			PublicPort:   443,
			TLS:          true,
		},
	})
	if err != nil {
		t.Fatalf("new admin server: %v", err)
	}
	return s, st
}

func seedCredential(t *testing.T, st *store.Store, accountName, clientName string) (*store.Account, *store.Client) {
	t.Helper()
	ctx := context.Background()

	acct, err := st.CreateAccount(ctx, accountName, 0)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	client := &store.Client{
		ID:         utils.GenerateShortID() + utils.GenerateShortID(),
		AccountID:  acct.ID,
		Name:       clientName,
		SecretHash: "hash",
		Enabled:    true,
	}
	if err := st.CreateClient(ctx, client); err != nil {
		t.Fatalf("create client: %v", err)
	}
	return acct, client
}

func provision(t *testing.T, s *Server, body string) (*httptest.ResponseRecorder, provisionView) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/provision", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleProvision(rec, req)

	var out provisionView
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return rec, out
}

func scriptFor(t *testing.T, view provisionView, platform string) provisionCommand {
	t.Helper()
	for _, cmd := range view.Commands {
		if cmd.Platform == platform {
			return cmd
		}
	}
	t.Fatalf("no command rendered for %s", platform)
	return provisionCommand{}
}

func TestProvisionRendersTheConfigurationCommand(t *testing.T) {
	s, st := newProvisionTestServer(t)
	_, client := seedCredential(t, st, "acme", "billing-box")

	rec, out := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_type": "http",
		"local_port": 8080,
		"subdomain": "billing"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	script := scriptFor(t, out, "linux").Script
	for _, want := range []string{
		"drip config set --server tunnel.example.com:443 --token " + tokenPlaceholder,
		"drip config tunnel add --name http-8080 --type http --port 8080 --subdomain billing --replace",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "drip start") {
		t.Errorf("autostart was not asked for but the script starts the tunnel:\n%s", script)
	}
	if out.URL != "https://billing.tunnel.example.com" {
		t.Errorf("url = %q", out.URL)
	}
	if !out.ReservationCreated {
		t.Error("the allocation was free, so it should have been created")
	}
	if out.Token != "" {
		t.Error("an existing credential must not hand back a token")
	}
}

func TestProvisionIssuesACredentialWhenAsked(t *testing.T) {
	s, st := newProvisionTestServer(t)
	acct, err := st.CreateAccount(context.Background(), "acme", 0)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	rec, out := provision(t, s, `{
		"new_client": {"account_id": "`+acct.ID+`", "name": "win-svc-01"},
		"tunnel_type": "http",
		"local_port": 3000
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if out.Token == "" {
		t.Fatal("a freshly issued credential must return its token exactly once")
	}
	if !strings.HasPrefix(out.Token, "drip_") {
		t.Errorf("token = %q, want a drip_ credential", out.Token)
	}

	script := scriptFor(t, out, "linux").Script
	if !strings.Contains(script, "--token "+out.Token) {
		t.Errorf("the real token should be embedded, not the placeholder:\n%s", script)
	}
	if strings.Contains(script, tokenPlaceholder) {
		t.Errorf("placeholder left in a script that has a real token:\n%s", script)
	}
	if out.Client.ID == "" || out.Client.Name != "win-svc-01" {
		t.Errorf("client = %+v", out.Client)
	}
}

func TestProvisionReusesAnAllocationTheAccountAlreadyHolds(t *testing.T) {
	s, st := newProvisionTestServer(t)
	acct, client := seedCredential(t, st, "acme", "billing-box")

	existing := &store.Reservation{
		AccountID:  acct.ID,
		TunnelType: store.TunnelTypeHTTP,
		Subdomain:  "billing",
		Enabled:    true,
	}
	if err := st.CreateReservation(context.Background(), existing); err != nil {
		t.Fatalf("create reservation: %v", err)
	}

	rec, out := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_type": "http",
		"local_port": 8080,
		"subdomain": "billing"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if out.ReservationCreated {
		t.Error("the allocation already existed, so it was adopted, not created")
	}
	if out.Reservation == nil || out.Reservation.ID != existing.ID {
		t.Fatalf("reservation = %+v, want the existing one", out.Reservation)
	}

	// An unbound allocation is bound to the machine being provisioned, or the
	// client would never land on it automatically.
	stored, err := st.GetReservation(context.Background(), existing.ID)
	if err != nil {
		t.Fatalf("read back reservation: %v", err)
	}
	if stored.ClientID == nil || *stored.ClientID != client.ID {
		t.Errorf("reservation client = %v, want %s", stored.ClientID, client.ID)
	}
}

func TestProvisionRefusesAnAllocationOwnedByAnotherAccount(t *testing.T) {
	s, st := newProvisionTestServer(t)
	_, mine := seedCredential(t, st, "acme", "billing-box")
	theirs, _ := seedCredential(t, st, "globex", "their-box")

	taken := &store.Reservation{
		AccountID:  theirs.ID,
		TunnelType: store.TunnelTypeHTTP,
		Subdomain:  "billing",
		Enabled:    true,
	}
	if err := st.CreateReservation(context.Background(), taken); err != nil {
		t.Fatalf("create reservation: %v", err)
	}

	rec, _ := provision(t, s, `{
		"client_id": "`+mine.ID+`",
		"tunnel_type": "http",
		"local_port": 8080,
		"subdomain": "billing"
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "another account") {
		t.Errorf("error should name the conflict: %s", rec.Body.String())
	}
}

func TestProvisionTCPBindsThePortWithoutNamingIt(t *testing.T) {
	s, st := newProvisionTestServer(t)
	_, client := seedCredential(t, st, "acme", "db-box")

	rec, out := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_type": "tcp",
		"local_port": 5432,
		"tcp_port": 20050
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// A TCP reservation is bound by the client asking for nothing, so a name on
	// the command line would be wrong.
	script := scriptFor(t, out, "linux").Script
	if strings.Contains(script, "--subdomain") {
		t.Errorf("a tcp tunnel must not request a name:\n%s", script)
	}
	if !strings.Contains(script, "--type tcp --port 5432") {
		t.Errorf("script missing the local tcp port:\n%s", script)
	}
	if out.Reservation == nil || out.Reservation.TCPPort != 20050 {
		t.Fatalf("reservation = %+v, want port 20050", out.Reservation)
	}
	if out.URL != "tcp://tunnel.example.com:20050" {
		t.Errorf("url = %q", out.URL)
	}
}

func TestProvisionRefusesANameOnATCPTunnel(t *testing.T) {
	s, st := newProvisionTestServer(t)
	_, client := seedCredential(t, st, "acme", "db-box")

	rec, _ := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_type": "tcp",
		"local_port": 5432,
		"subdomain": "database"
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestProvisionAutostartInstallsAWindowsServiceWithAFreshConfigCopy(t *testing.T) {
	s, st := newProvisionTestServer(t)
	_, client := seedCredential(t, st, "acme", "win-svc-01")

	rec, out := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_name": "billing",
		"tunnel_type": "http",
		"local_port": 8080,
		"subdomain": "billing",
		"autostart": true
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	windows := scriptFor(t, out, "windows")
	// The step before this one rewrote the user config, so the machine-wide
	// copy the service reads has to be reseeded or it stays stale.
	if !strings.Contains(windows.Script, "drip service install --tunnel billing --reseed") {
		t.Errorf("windows autostart missing a reseeded service install:\n%s", windows.Script)
	}
	if !strings.Contains(windows.Script, "drip service start") {
		t.Errorf("installing a service does not start it, so the script must:\n%s", windows.Script)
	}
	if !windows.Elevated {
		t.Error("registering a service needs an elevated prompt")
	}
	if strings.Contains(windows.Script, "&&") {
		t.Errorf("Windows PowerShell 5.1 has no &&:\n%s", windows.Script)
	}

	unix := scriptFor(t, out, "linux")
	if !strings.Contains(unix.Script, "drip start --all") {
		t.Errorf("unix autostart missing the run step:\n%s", unix.Script)
	}
	if unix.Elevated {
		t.Error("nothing on unix needs elevation")
	}
}

func TestProvisionQuotesValuesThatWouldOtherwiseBeShellSyntax(t *testing.T) {
	s, st := newProvisionTestServer(t)
	_, client := seedCredential(t, st, "acme", "odd-box")

	rec, out := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_name": "it's a; tunnel",
		"tunnel_type": "http",
		"local_port": 8080
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	unix := scriptFor(t, out, "linux").Script
	if !strings.Contains(unix, `--name 'it'\''s a; tunnel'`) {
		t.Errorf("sh quoting is wrong:\n%s", unix)
	}

	windows := scriptFor(t, out, "windows").Script
	if !strings.Contains(windows, `--name 'it''s a; tunnel'`) {
		t.Errorf("powershell quoting is wrong:\n%s", windows)
	}
}

func TestProvisionRefusesControlCharactersInAName(t *testing.T) {
	s, st := newProvisionTestServer(t)
	_, client := seedCredential(t, st, "acme", "odd-box")

	rec, _ := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_name": "billing\nrm -rf /",
		"tunnel_type": "http",
		"local_port": 8080
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestProvisionNeedsExactlyOneCredentialSource(t *testing.T) {
	s, st := newProvisionTestServer(t)
	_, client := seedCredential(t, st, "acme", "billing-box")

	cases := map[string]string{
		"neither": `{"tunnel_type": "http", "local_port": 8080}`,
		"both": `{"client_id": "` + client.ID + `", "new_client": {"account_id": "x", "name": "y"},
			"tunnel_type": "http", "local_port": 8080}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec, _ := provision(t, s, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// newRangedTestServer is newProvisionTestServer with the deployment reporting
// the TCP port range its allocator actually serves.
func newRangedTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	s, st := newProvisionTestServer(t)
	s.deployment.TCPPortMin = 33000
	s.deployment.TCPPortMax = 33020
	return s, st
}

func TestProvisionRefusesATCPPortOutsideTheServerRange(t *testing.T) {
	s, st := newRangedTestServer(t)
	_, client := seedCredential(t, st, "acme", "db-box")

	rec, _ := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_type": "tcp",
		"local_port": 5432,
		"tcp_port": 20050
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "33000-33020") {
		t.Errorf("the error should name the range: %s", rec.Body.String())
	}

	// Nothing may be written for a port the server could never allocate.
	list, err := st.ListReservations(context.Background(), "")
	if err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("reservations = %d, want none", len(list))
	}
}

func TestProvisionAcceptsATCPPortInsideTheServerRange(t *testing.T) {
	s, st := newRangedTestServer(t)
	_, client := seedCredential(t, st, "acme", "db-box")

	rec, out := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_type": "tcp",
		"local_port": 5432,
		"tcp_port": 33005
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if out.Reservation == nil || out.Reservation.TCPPort != 33005 {
		t.Fatalf("reservation = %+v, want port 33005", out.Reservation)
	}
}

// A deployment that never reported its range must not have allocations refused
// on information it did not give.
func TestProvisionSkipsTheRangeCheckWhenTheServerDidNotReportOne(t *testing.T) {
	s, st := newProvisionTestServer(t)
	_, client := seedCredential(t, st, "acme", "db-box")

	rec, _ := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_type": "tcp",
		"local_port": 5432,
		"tcp_port": 20050
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// The builder refuses an out-of-range port it would only have adopted, too:
// the command would promise an address the server cannot serve.
func TestProvisionRefusesAdoptingAnOutOfRangeAllocation(t *testing.T) {
	s, st := newRangedTestServer(t)
	acct, client := seedCredential(t, st, "acme", "db-box")

	stale := &store.Reservation{
		AccountID:  acct.ID,
		TunnelType: store.TunnelTypeTCP,
		TCPPort:    20050,
		Enabled:    true,
	}
	if err := st.CreateReservation(context.Background(), stale); err != nil {
		t.Fatalf("create reservation: %v", err)
	}

	rec, _ := provision(t, s, `{
		"client_id": "`+client.ID+`",
		"tunnel_type": "tcp",
		"local_port": 5432,
		"tcp_port": 20050
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCreateReservationRefusesATCPPortOutsideTheServerRange(t *testing.T) {
	s, st := newRangedTestServer(t)
	acct, err := st.CreateAccount(context.Background(), "acme", 0)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/reservations",
		strings.NewReader(`{"account_id":"`+acct.ID+`","tcp_port":20050}`))
	rec := httptest.NewRecorder()
	s.handleCreateReservation(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "33000-33020") {
		t.Errorf("the error should name the range: %s", rec.Body.String())
	}
}

func TestServerInfoReportsTheTCPPortRange(t *testing.T) {
	s, _ := newRangedTestServer(t)

	rec := httptest.NewRecorder()
	s.handleServerInfo(rec, httptest.NewRequest(http.MethodGet, "/api/server", nil))

	var out serverInfoView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.TCPPortMin != 33000 || out.TCPPortMax != 33020 {
		t.Errorf("range = %d-%d, want 33000-33020", out.TCPPortMin, out.TCPPortMax)
	}
}

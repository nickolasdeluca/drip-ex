package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"drip/internal/server/auth"
	"drip/internal/server/store"
)

// tokenPlaceholder stands in for a credential the panel cannot know.
//
// Only sha256(secret) is stored, so an existing credential's token is gone the
// moment its dialog closes; the builder renders everything else and leaves this
// for the operator to paste. It is deliberately free of shell metacharacters:
// a placeholder like <TOKEN> would be a redirection in sh.
const tokenPlaceholder = "PASTE_TOKEN_HERE"

// provisionRequest describes the machine an operator is about to connect.
//
// Exactly one of ClientID and NewClient identifies the credential: an existing
// one renders with the placeholder, a fresh one renders with the real token,
// which is returned here and nowhere else.
type provisionRequest struct {
	ClientID  string `json:"client_id"`
	NewClient *struct {
		AccountID string `json:"account_id"`
		Name      string `json:"name"`
		Bandwidth string `json:"bandwidth"`
	} `json:"new_client"`

	TunnelName   string `json:"tunnel_name"`
	TunnelType   string `json:"tunnel_type"`
	LocalPort    int    `json:"local_port"`
	LocalAddress string `json:"local_address"`

	// Subdomain (http/https) or TCPPort (tcp) is the allocation this machine
	// should land on. Either is created when free and reused when the account
	// already holds it. Both empty leaves the machine on whatever resolves for
	// its credential.
	Subdomain string `json:"subdomain"`
	TCPPort   int    `json:"tcp_port"`
	Bandwidth string `json:"bandwidth"`

	// Autostart appends the step that leaves the tunnel running: 'drip start'
	// on Unix, a Windows service on Windows.
	Autostart bool `json:"autostart"`
}

// provisionCommand is one platform's rendering of the same plan.
type provisionCommand struct {
	Platform string `json:"platform"`
	Shell    string `json:"shell"`
	Script   string `json:"script"`
	// Elevated reports that the script needs an administrator prompt. Only the
	// Windows service step does.
	Elevated bool `json:"elevated"`
}

type provisionView struct {
	Client      clientView       `json:"client"`
	Reservation *reservationView `json:"reservation,omitempty"`
	// ReservationCreated separates a name this call allocated from one the
	// account already held, so the panel can say which happened.
	ReservationCreated bool `json:"reservation_created"`
	// Token is present only when this call issued the credential.
	Token      string             `json:"token,omitempty"`
	URL        string             `json:"url,omitempty"`
	TunnelName string             `json:"tunnel_name"`
	Commands   []provisionCommand `json:"commands"`
}

// handleProvision builds the configuration command for one machine.
//
// It is the second half of a two-step deployment: the operator installs the
// client binary however that fleet installs binaries, then pastes this. The
// panel never renders an installer, so nothing here depends on how drip got
// onto the host.
func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	var req provisionRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	plan, err := s.planProvision(r, &req)
	if err != nil {
		writeError(w, plan.status, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, plan.view())
}

// provisionPlan is a validated request plus everything resolved for it.
type provisionPlan struct {
	status int

	server     string
	token      string
	tunnelName string
	tunnelType string
	localPort  int
	localAddr  string
	subdomain  string
	autostart  bool

	client      *store.Client
	reservation *store.Reservation
	created     bool
	issuedToken string
	url         string
}

func (s *Server) planProvision(r *http.Request, req *provisionRequest) (provisionPlan, error) {
	plan := provisionPlan{status: http.StatusBadRequest}

	tunnelType := store.NormalizeTunnelType(req.TunnelType)
	switch tunnelType {
	case store.TunnelTypeHTTP, store.TunnelTypeTCP:
	case "":
		tunnelType = store.TunnelTypeHTTP
	default:
		return plan, fmt.Errorf("unknown tunnel type %q", req.TunnelType)
	}
	// The reservation family is normalized, but the client config keeps the
	// protocol the operator picked: https is a real tunnel type there.
	configType := strings.ToLower(strings.TrimSpace(req.TunnelType))
	if configType == "" {
		configType = store.TunnelTypeHTTP
	}

	if req.LocalPort < 1 || req.LocalPort > 65535 {
		return plan, fmt.Errorf("a local port between 1 and 65535 is required")
	}

	subdomain := strings.ToLower(strings.TrimSpace(req.Subdomain))
	if tunnelType == store.TunnelTypeTCP && subdomain != "" {
		return plan, fmt.Errorf("a tcp tunnel reserves its port, not a name")
	}
	if tunnelType == store.TunnelTypeHTTP && req.TCPPort != 0 {
		return plan, fmt.Errorf("an http tunnel reserves a name, not a port")
	}
	// Checked before the allocation is resolved, so an out-of-range port is
	// refused whether it would be created here or adopted from an earlier one.
	// A command promising a port the server cannot allocate is worse than no
	// command at all.
	if req.TCPPort != 0 {
		if err := s.checkTCPPortRange(req.TCPPort); err != nil {
			return plan, err
		}
	}

	localAddr := strings.TrimSpace(req.LocalAddress)
	if err := printableArg("local address", localAddr); err != nil {
		return plan, err
	}

	name := strings.TrimSpace(req.TunnelName)
	if name == "" {
		name = fmt.Sprintf("%s-%d", configType, req.LocalPort)
	}
	if err := printableArg("tunnel name", name); err != nil {
		return plan, err
	}

	client, issued, err := s.provisionClient(r, req)
	if err != nil {
		plan.status = storeStatus(err)
		return plan, err
	}

	reservation, created, err := s.resolveReservation(r, client, tunnelType, subdomain, req.TCPPort, strings.TrimSpace(req.Bandwidth))
	if err != nil {
		plan.status = storeStatus(err)
		return plan, err
	}

	// A reservation the client will bind by name is what goes on the command
	// line; a TCP port is bound by the client asking for nothing, so it stays
	// off it. See the resolution order in internal/server/reservations.
	requested := ""
	if reservation != nil && reservation.Subdomain != "" {
		requested = reservation.Subdomain
	}

	token := tokenPlaceholder
	if issued != "" {
		token = issued
	}

	plan = provisionPlan{
		status:      http.StatusOK,
		server:      s.clientEndpoint(),
		token:       token,
		tunnelName:  name,
		tunnelType:  configType,
		localPort:   req.LocalPort,
		localAddr:   localAddr,
		subdomain:   requested,
		autostart:   req.Autostart,
		client:      client,
		reservation: reservation,
		created:     created,
		issuedToken: issued,
	}
	if reservation != nil {
		plan.url = s.allocationURL(reservation)
	}

	s.audit(r, "client.provision", "client", client.ID, name)
	return plan, nil
}

// provisionClient returns the credential the command configures. The token
// comes back only when this call created it.
func (s *Server) provisionClient(r *http.Request, req *provisionRequest) (*store.Client, string, error) {
	if (strings.TrimSpace(req.ClientID) == "") == (req.NewClient == nil) {
		return nil, "", fmt.Errorf("provide exactly one of client_id or new_client")
	}

	if req.NewClient == nil {
		client, err := s.store.GetClient(r.Context(), strings.TrimSpace(req.ClientID))
		if err != nil {
			return nil, "", err
		}
		return client, "", nil
	}

	cred, err := auth.GenerateCredential()
	if err != nil {
		return nil, "", fmt.Errorf("generate credential: %w", err)
	}

	client := &store.Client{
		ID:         cred.ID,
		AccountID:  strings.TrimSpace(req.NewClient.AccountID),
		Name:       strings.TrimSpace(req.NewClient.Name),
		SecretHash: auth.HashSecret(cred.Secret),
		Enabled:    true,
		Bandwidth:  strings.TrimSpace(req.NewClient.Bandwidth),
	}
	if err := s.store.CreateClient(r.Context(), client); err != nil {
		return nil, "", err
	}

	s.audit(r, "client.create", "client", client.ID, client.Name)
	return client, cred.String(), nil
}

// resolveReservation allocates the name or port this machine should land on,
// reusing one the account already holds rather than failing on the conflict.
//
// Reuse is what makes the builder safe to run twice: rebuilding the command for
// a machine that is already deployed must not need the operator to know whether
// the allocation exists.
func (s *Server) resolveReservation(
	r *http.Request,
	client *store.Client,
	tunnelType, subdomain string,
	tcpPort int,
	bandwidth string,
) (*store.Reservation, bool, error) {
	if subdomain == "" && tcpPort == 0 {
		return nil, false, nil
	}

	var (
		existing *store.Reservation
		err      error
	)
	if subdomain != "" {
		existing, err = s.store.GetReservationBySubdomain(r.Context(), subdomain)
	} else {
		existing, err = s.store.GetReservationByTCPPort(r.Context(), tcpPort)
	}
	if err == nil && existing != nil {
		return s.adoptReservation(r, client, existing)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, err
	}

	clientID := client.ID
	reservation := &store.Reservation{
		AccountID:  client.AccountID,
		ClientID:   &clientID,
		TunnelType: tunnelType,
		Subdomain:  subdomain,
		TCPPort:    tcpPort,
		Bandwidth:  bandwidth,
		Enabled:    true,
	}
	if err := s.store.CreateReservation(r.Context(), reservation); err != nil {
		return nil, false, err
	}

	s.audit(r, "reservation.create", "reservation", reservation.ID, reservation.Target())
	return reservation, true, nil
}

// adoptReservation points an allocation the account already holds at this
// machine. An allocation owned elsewhere is refused rather than stolen.
func (s *Server) adoptReservation(r *http.Request, client *store.Client, reservation *store.Reservation) (*store.Reservation, bool, error) {
	if reservation.AccountID != client.AccountID {
		return nil, false, fmt.Errorf("%s is allocated to another account", reservation.Target())
	}
	if reservation.ClientID != nil && *reservation.ClientID != "" && *reservation.ClientID != client.ID {
		return nil, false, fmt.Errorf("%s is already bound to another machine", reservation.Target())
	}

	if reservation.ClientID == nil || *reservation.ClientID == "" {
		clientID := client.ID
		reservation.ClientID = &clientID
		if err := s.store.UpdateReservation(r.Context(), reservation); err != nil {
			return nil, false, err
		}
		s.audit(r, "reservation.update", "reservation", reservation.ID, reservation.Target())
	}
	return reservation, false, nil
}

// ---- rendering ----

func (p provisionPlan) view() provisionView {
	out := provisionView{
		Client:             toClientView(p.client),
		ReservationCreated: p.created,
		Token:              p.issuedToken,
		URL:                p.url,
		TunnelName:         p.tunnelName,
		Commands:           p.commands(),
	}
	if p.reservation != nil {
		view := toReservationView(p.reservation)
		out.Reservation = &view
	}
	return out
}

// steps are the drip invocations, in order, that turn a freshly installed
// binary into a configured machine. Every platform runs the same ones; only
// the last step and the quoting differ.
func (p provisionPlan) steps() [][]string {
	set := []string{"drip", "config", "set", "--server", p.server, "--token", p.token}

	add := []string{
		"drip", "config", "tunnel", "add",
		"--name", p.tunnelName,
		"--type", p.tunnelType,
		"--port", strconv.Itoa(p.localPort),
	}
	if p.localAddr != "" {
		add = append(add, "--address", p.localAddr)
	}
	if p.subdomain != "" {
		add = append(add, "--subdomain", p.subdomain)
	}
	// Rebuilding the command for a machine that already ran it must not fail
	// on the tunnel name it wrote last time.
	add = append(add, "--replace")

	return [][]string{set, add}
}

func (p provisionPlan) commands() []provisionCommand {
	steps := p.steps()

	unix := make([][]string, len(steps))
	copy(unix, steps)
	if p.autostart {
		unix = append(unix, []string{"drip", "start", "--all"})
	}
	script := joinShell(unix)

	windows := make([][]string, len(steps))
	copy(windows, steps)
	if p.autostart {
		// --reseed is mandatory here: install keeps an existing machine-wide
		// config copy, and the step above just rewrote the user one, so
		// without it the service would read a stale file.
		windows = append(windows,
			[]string{"drip", "service", "install", "--tunnel", p.tunnelName, "--reseed"},
			[]string{"drip", "service", "start"},
		)
	}

	return []provisionCommand{
		{Platform: "linux", Shell: "sh", Script: script},
		{Platform: "macos", Shell: "sh", Script: script},
		{Platform: "windows", Shell: "powershell", Script: joinPowerShell(windows), Elevated: p.autostart},
	}
}

// joinShell chains the steps with && so a failure stops the sequence instead of
// leaving the machine half configured.
func joinShell(steps [][]string) string {
	lines := make([]string, 0, len(steps))
	for _, step := range steps {
		args := make([]string, 0, len(step))
		for _, arg := range step {
			args = append(args, shQuote(arg))
		}
		lines = append(lines, strings.Join(args, " "))
	}
	return strings.Join(lines, " \\\n  && ")
}

// joinPowerShell keeps one command per line. PowerShell's && needs version 7,
// and these hosts are often on Windows PowerShell 5.1.
func joinPowerShell(steps [][]string) string {
	lines := make([]string, 0, len(steps))
	for _, step := range steps {
		args := make([]string, 0, len(step))
		for _, arg := range step {
			args = append(args, psQuote(arg))
		}
		lines = append(lines, strings.Join(args, " "))
	}
	return strings.Join(lines, "\n")
}

// shSafe and psSafe are the characters that need no quoting in each shell.
// Everything else is quoted, so a value the operator typed can never become
// syntax. PowerShell's set is the tighter one: @ starts a splat, % is an alias
// for ForEach-Object and , builds an array.
const (
	shSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%_+=:,./-"
	psSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_+=:./-"
)

func safeIn(set, s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(set, r) {
			return false
		}
	}
	return true
}

func shQuote(s string) string {
	if safeIn(shSafe, s) {
		return s
	}
	// A single-quoted string ends, escapes the quote, and reopens.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func psQuote(s string) string {
	if safeIn(psSafe, s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// printableArg refuses control characters. Quoting would contain them, but a
// newline in a rendered command is unreadable and invites a copy that runs only
// half of it.
func printableArg(what, value string) error {
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s cannot contain control characters", what)
		}
	}
	return nil
}

// clientEndpoint is the address a client dials, in the shape 'drip config set'
// takes.
func (s *Server) clientEndpoint() string {
	d := s.deployment
	port := d.PublicPort
	if port == 0 {
		port = 443
	}
	domain := d.Domain
	if domain == "" {
		domain = d.TunnelDomain
	}
	return fmt.Sprintf("%s:%d", domain, port)
}

// allocationURL mirrors the panel's own URL builder so the address the command
// lands on is spelled the same way everywhere.
func (s *Server) allocationURL(reservation *store.Reservation) string {
	d := s.deployment
	domain := d.TunnelDomain
	if domain == "" {
		domain = d.Domain
	}
	if domain == "" {
		return ""
	}

	port := d.PublicPort
	if port == 0 {
		port = 443
	}

	if reservation.Subdomain != "" {
		if port == 443 {
			return fmt.Sprintf("https://%s.%s", reservation.Subdomain, domain)
		}
		return fmt.Sprintf("https://%s.%s:%d", reservation.Subdomain, domain, port)
	}
	return fmt.Sprintf("tcp://%s:%d", domain, reservation.TCPPort)
}

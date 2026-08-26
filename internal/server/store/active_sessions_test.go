package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateSessionNormalizesAndDefaults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	acct := seedAccount(t, s, "acme")

	sess := &Session{
		AccountID:  acct.ID,
		ClientID:   "client-1",
		TunnelType: TunnelTypeHTTPS,
		Subdomain:  "  Billing  ",
		LocalPort:  9765,
		RemoteIP:   "203.0.113.7",
	}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if sess.ID == "" {
		t.Fatal("CreateSession() left the ID empty")
	}
	if sess.StartedAt.IsZero() {
		t.Fatal("CreateSession() left StartedAt zero")
	}

	got, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if got.Subdomain != "billing" {
		t.Errorf("Subdomain = %q, want billing", got.Subdomain)
	}
	// http and https share one family, exactly as reservations do.
	if got.TunnelType != TunnelTypeHTTP {
		t.Errorf("TunnelType = %q, want %q", got.TunnelType, TunnelTypeHTTP)
	}
	if got.LocalPort != 9765 || got.RemoteIP != "203.0.113.7" {
		t.Errorf("session = %+v, want local port 9765 from 203.0.113.7", got)
	}
	if got.ReservationID != nil {
		t.Errorf("ReservationID = %v, want nil", *got.ReservationID)
	}
}

// Anonymous and legacy-token tunnels have no identity, and the panel still has
// to list them.
func TestCreateSessionAllowsMissingIdentity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sess := &Session{TunnelType: TunnelTypeHTTP, Subdomain: "loose"}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	got, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if got.AccountID != "" || got.ClientID != "" {
		t.Errorf("session = %+v, want empty identity", got)
	}
}

func TestCreateSessionNeedsATarget(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession(context.Background(), &Session{TunnelType: TunnelTypeHTTP}); err == nil {
		t.Fatal("CreateSession() with no subdomain or port succeeded, want error")
	}
}

func TestCreateSessionRejectsDuplicateSubdomain(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first := &Session{TunnelType: TunnelTypeHTTP, Subdomain: "billing"}
	if err := s.CreateSession(ctx, first); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	err := s.CreateSession(ctx, &Session{TunnelType: TunnelTypeHTTP, Subdomain: "billing"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateSession() error = %v, want ErrConflict", err)
	}
}

func TestListSessionsFiltersByAccount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	acme := seedAccount(t, s, "acme")
	other := seedAccount(t, s, "other")

	for _, sess := range []*Session{
		{AccountID: acme.ID, ClientID: "c1", TunnelType: TunnelTypeHTTP, Subdomain: "one"},
		{AccountID: other.ID, ClientID: "c2", TunnelType: TunnelTypeHTTP, Subdomain: "two"},
	} {
		if err := s.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession() error = %v", err)
		}
	}

	all, err := s.ListSessions(ctx, "")
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListSessions(all) returned %d sessions, want 2", len(all))
	}

	mine, err := s.ListSessions(ctx, acme.ID)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(mine) != 1 || mine[0].Subdomain != "one" {
		t.Fatalf("ListSessions(acme) = %+v, want just \"one\"", mine)
	}
}

func TestSetSessionReservation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	acct := seedAccount(t, s, "acme")

	sess := &Session{AccountID: acct.ID, ClientID: "c1", TunnelType: TunnelTypeHTTP, Subdomain: "billing"}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	res := &Reservation{AccountID: acct.ID, TunnelType: TunnelTypeHTTP, Subdomain: "billing", Enabled: true}
	if err := s.CreateReservation(ctx, res); err != nil {
		t.Fatalf("CreateReservation() error = %v", err)
	}

	if err := s.SetSessionReservation(ctx, sess.ID, res.ID); err != nil {
		t.Fatalf("SetSessionReservation() error = %v", err)
	}

	got, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if got.ReservationID == nil || *got.ReservationID != res.ID {
		t.Fatalf("ReservationID = %v, want %s", got.ReservationID, res.ID)
	}

	if err := s.SetSessionReservation(ctx, "nope", res.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetSessionReservation(missing) error = %v, want ErrNotFound", err)
	}
}

// Teardown must not fail over bookkeeping, so deleting a row that is already
// gone is a no-op.
func TestDeleteSessionIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sess := &Session{TunnelType: TunnelTypeHTTP, Subdomain: "billing"}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := s.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	if err := s.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession() second call error = %v", err)
	}
	if _, err := s.GetSession(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession() error = %v, want ErrNotFound", err)
	}
}

// A session row describes this process. Whatever the last run left behind is
// stale, and the name it holds must be free again.
func TestPurgeSessionsClearsPreviousRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateSession(ctx, &Session{TunnelType: TunnelTypeHTTP, Subdomain: "billing"}); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := s.PurgeSessions(ctx); err != nil {
		t.Fatalf("PurgeSessions() error = %v", err)
	}

	list, err := s.ListSessions(ctx, "")
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListSessions() returned %d sessions after purge, want 0", len(list))
	}

	if err := s.CreateSession(ctx, &Session{TunnelType: TunnelTypeHTTP, Subdomain: "billing"}); err != nil {
		t.Fatalf("CreateSession() after purge error = %v", err)
	}
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"drip/internal/shared/utils"
)

const sessionColumns = `id, account_id, client_id, reservation_id, tunnel_type,
	subdomain, tcp_port, local_port, remote_ip, started_at`

// CreateSession records a tunnel that has just gone live.
//
// The row mirrors what the tunnel manager holds in memory, plus the fields the
// manager never sees: which reservation was bound, the client's local port and
// when the tunnel started. Anonymous and legacy-token registrations have no
// identity, so AccountID and ClientID are stored empty rather than rejected —
// the panel still has to show them, it just cannot pin them.
func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	if sess == nil {
		return fmt.Errorf("session is required")
	}

	sess.TunnelType = NormalizeTunnelType(sess.TunnelType)
	sess.Subdomain = strings.ToLower(strings.TrimSpace(sess.Subdomain))
	if sess.Subdomain == "" && sess.TCPPort == 0 {
		return fmt.Errorf("a session needs a subdomain or a TCP port")
	}

	if sess.ID == "" {
		sess.ID = utils.GenerateID()
	}
	if sess.StartedAt.IsZero() {
		sess.StartedAt = time.Now()
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO active_sessions (id, account_id, client_id, reservation_id,
			tunnel_type, subdomain, tcp_port, local_port, remote_ip, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.AccountID, sess.ClientID, nullString(sess.ReservationID),
		sess.TunnelType, sess.Subdomain, sess.TCPPort, sess.LocalPort,
		sess.RemoteIP, sess.StartedAt.Unix())
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("session for subdomain %s: %w", sess.Subdomain, ErrConflict)
		}
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// GetSession looks up a live session by ID.
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM active_sessions WHERE id = ?`, id)
	return scanSession(row)
}

// ListSessions returns the live sessions, optionally filtered to one account,
// newest first.
func (s *Store) ListSessions(ctx context.Context, accountID string) ([]*Session, error) {
	query := `SELECT ` + sessionColumns + ` FROM active_sessions`
	args := []interface{}{}
	if accountID != "" {
		query += ` WHERE account_id = ?`
		args = append(args, accountID)
	}
	query += ` ORDER BY started_at DESC, id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read sessions: %w", err)
	}
	return out, nil
}

// DeleteSession removes a session when its tunnel goes away. A missing row is
// not an error: the tunnel may have been recorded before the store existed, and
// teardown must not fail over bookkeeping.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM active_sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

// SetSessionReservation links a live session to the reservation it now holds.
// Used by the pin flow, where the reservation is created after the tunnel is
// already up.
func (s *Store) SetSessionReservation(ctx context.Context, sessionID, reservationID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE active_sessions SET reservation_id = ? WHERE id = ?`,
		nullString(&reservationID), sessionID)
	if err != nil {
		return fmt.Errorf("failed to link session to reservation: %w", err)
	}
	return checkAffected(res, "session")
}

// PurgeSessions empties the table. Sessions describe what is live in *this*
// process, so anything left from a previous run is a lie; the server calls this
// at startup before accepting registrations.
func (s *Store) PurgeSessions(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM active_sessions`); err != nil {
		return fmt.Errorf("failed to purge sessions: %w", err)
	}
	return nil
}

// Target returns a human-readable description of what this session occupies.
func (sess *Session) Target() string {
	if sess.Subdomain != "" {
		return "subdomain " + sess.Subdomain
	}
	return fmt.Sprintf("tcp port %d", sess.TCPPort)
}

func scanSession(row rowScanner) (*Session, error) {
	var (
		sess          Session
		reservationID sql.NullString
		startedAt     int64
	)
	err := row.Scan(&sess.ID, &sess.AccountID, &sess.ClientID, &reservationID,
		&sess.TunnelType, &sess.Subdomain, &sess.TCPPort, &sess.LocalPort,
		&sess.RemoteIP, &startedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan session: %w", err)
	}
	sess.ReservationID = strPtr(reservationID)
	sess.StartedAt = time.Unix(startedAt, 0)
	return &sess, nil
}

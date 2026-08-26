package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"drip/internal/server/store"
	"drip/internal/server/tunnel"

	"go.uber.org/zap"
)

// handlePinSession turns a running tunnel into a reservation.
//
// This is the second of the two reservation paths: instead of reserving a name
// up front and waiting for a client to bind it, an operator watches a tunnel
// come up in the panel and pins what it already holds. Both paths write the
// same tunnel_reservations row.
//
// The pin takes effect on the next reconnect. The live tunnel keeps the name it
// registered with — it was resolved before the reservation existed — so a pin
// under a different name is a rename that lands when the client comes back.
func (s *Server) handlePinSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Subdomain string `json:"subdomain"`
		Bandwidth string `json:"bandwidth"`
	}
	if r.ContentLength > 0 {
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	sess, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, storeStatus(err), err.Error())
		return
	}

	// A reservation belongs to an account and binds a credential. Anonymous and
	// legacy shared-token registrations have neither, and handing their tunnel
	// to an account would invent an owner the client cannot prove.
	if sess.AccountID == "" || sess.ClientID == "" {
		writeError(w, http.StatusBadRequest,
			"this tunnel registered without a client credential; issue one for this machine and reconnect before pinning")
		return
	}

	if sess.ReservationID != nil && *sess.ReservationID != "" {
		writeError(w, http.StatusConflict, "this tunnel already bound a reservation")
		return
	}

	clientID := sess.ClientID
	reservation := &store.Reservation{
		AccountID:  sess.AccountID,
		ClientID:   &clientID,
		TunnelType: sess.TunnelType,
		Bandwidth:  strings.TrimSpace(req.Bandwidth),
		Enabled:    true,
	}

	name := strings.ToLower(strings.TrimSpace(req.Subdomain))
	switch store.NormalizeTunnelType(sess.TunnelType) {
	case store.TunnelTypeTCP:
		if name != "" {
			writeError(w, http.StatusBadRequest,
				"a tcp tunnel reserves its port, not a name")
			return
		}
		reservation.TCPPort = sess.TCPPort
	default:
		if name == "" {
			name = sess.Subdomain
		}
		reservation.Subdomain = name
	}

	if err := s.checkReservationClient(r, reservation); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.CreateReservation(r.Context(), reservation); err != nil {
		writeError(w, storeStatus(err), err.Error())
		return
	}

	// Only a pin that kept the name describes the tunnel that is live now; a
	// rename points somewhere the client has not been yet.
	renamed := reservation.Subdomain != "" && reservation.Subdomain != sess.Subdomain
	if !renamed {
		if err := s.store.SetSessionReservation(r.Context(), sess.ID, reservation.ID); err != nil {
			s.logger.Warn("Failed to link session to its new reservation",
				zap.String("session_id", sess.ID),
				zap.String("reservation_id", reservation.ID),
				zap.Error(err),
			)
		}
	}

	rebound := s.rebind(sess, reservation)

	s.audit(r, "reservation.pin", "reservation", reservation.ID, reservation.Target())

	writeJSON(w, http.StatusCreated, struct {
		reservationView
		Renamed bool `json:"renamed"`
		// Rebound reports that the client was told to move now. False means
		// the allocation waits for the client's next reconnect, either because
		// it predates control streams or because the message did not land.
		Rebound bool `json:"rebound"`
	}{
		reservationView: toReservationView(reservation),
		Renamed:         renamed,
		Rebound:         rebound,
	})
}

// rebind asks the live client to reconnect onto what was just reserved for it,
// so an operator does not have to reach the machine to restart a service.
//
// It is best effort by design: a client that predates control streams has no
// way to hear it, and the reservation is still correct — it simply waits for
// the next reconnect.
func (s *Server) rebind(sess *store.Session, reservation *store.Reservation) bool {
	if s.manager == nil {
		return false
	}

	conn, live := s.manager.Get(sess.Subdomain)
	if !live || conn == nil {
		return false
	}

	target := reservation.Subdomain
	if reservation.TCPPort != 0 {
		// TCP reservations pin a port, and a client asks for one by name.
		target = fmt.Sprintf("tcp-%d", reservation.TCPPort)
	}

	if err := conn.Rebind(target, "pinned from the admin panel"); err != nil {
		if !errors.Is(err, tunnel.ErrNoControlStream) {
			s.logger.Warn("Failed to rebind a pinned tunnel",
				zap.String("subdomain", sess.Subdomain),
				zap.String("target", target),
				zap.Error(err),
			)
		}
		return false
	}

	s.logger.Info("Pinned tunnel asked to rebind",
		zap.String("subdomain", sess.Subdomain),
		zap.String("target", target),
	)
	return true
}

// handleListSessions returns the live tunnels as the control plane recorded
// them. The manager is the authority on what is connected, so rows it does not
// know about are dropped rather than reported as live.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	out := make([]sessionView, 0)

	list, err := s.store.ListSessions(r.Context(), r.URL.Query().Get("account_id"))
	if err != nil {
		s.internal(w, "list sessions", err)
		return
	}

	for _, sess := range list {
		if s.manager != nil {
			if _, live := s.manager.Get(sess.Subdomain); !live {
				continue
			}
		}
		out = append(out, toSessionView(sess))
	}
	writeJSON(w, http.StatusOK, out)
}

// sessionsBySubdomain indexes the recorded sessions for the tunnel listing.
// A store failure is not fatal there: the manager already answers the question
// the panel asked, and the extra fields are decoration on top of it.
func (s *Server) sessionsBySubdomain(r *http.Request) map[string]*store.Session {
	list, err := s.store.ListSessions(r.Context(), "")
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.logger.Warn("Failed to read live sessions", zap.Error(err))
		}
		return nil
	}

	out := make(map[string]*store.Session, len(list))
	for _, sess := range list {
		out[sess.Subdomain] = sess
	}
	return out
}

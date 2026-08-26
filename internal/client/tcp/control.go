package tcp

import (
	json "github.com/goccy/go-json"
	"go.uber.org/zap"

	"drip/internal/shared/protocol"
)

// controlLoop opens the control stream and acts on what the server sends.
//
// The client opens it, never the server: the server opens a stream per proxied
// request, so a channel it opened could not be told apart from traffic. Opening
// it here also means only an established, authenticated session can carry
// control messages.
func (c *PoolClient) controlLoop(h *sessionHandle) {
	defer c.wg.Done()

	if h == nil || h.session == nil {
		return
	}

	stream, err := h.session.Open()
	if err != nil {
		c.logger.Debug("Control stream unavailable", zap.Error(err))
		return
	}
	defer func() { _ = stream.Close() }()

	go func() {
		<-c.stopCh
		_ = stream.Close()
	}()

	for {
		frame, err := protocol.ReadFrame(stream)
		if err != nil {
			return
		}
		if c.handleControlFrame(frame) {
			return
		}
	}
}

// handleControlFrame acts on one control frame and reports whether the control
// stream is finished. It releases the frame.
func (c *PoolClient) handleControlFrame(frame *protocol.Frame) bool {
	defer frame.Release()

	switch frame.Type {
	case protocol.FrameTypeRebind:
		var req protocol.RebindRequest
		if err := json.Unmarshal(frame.Payload, &req); err != nil {
			c.logger.Warn("Ignoring malformed rebind", zap.Error(err))
			return false
		}

		c.logger.Info("Server asked for a rebind",
			zap.String("from", c.subdomain),
			zap.String("to", req.Subdomain),
			zap.String("reason", req.Reason),
		)

		// The name is decided at registration, so the only way to act on this
		// is to register again. The supervisor reconnects and reads
		// PendingRebind to know what to ask for.
		c.mu.Lock()
		c.rebindTo = req.Subdomain
		c.rebindSet = true
		c.mu.Unlock()

		_ = c.Close()
		return true

	default:
		c.logger.Debug("Ignoring unexpected control frame",
			zap.String("type", frame.Type.String()),
		)
		return false
	}
}

// PendingRebind reports the name the server asked this client to take next.
// The empty string with ok true means "ask for nothing", which is how a client
// lands on whatever reservation the server resolves for it.
func (c *PoolClient) PendingRebind() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rebindTo, c.rebindSet
}

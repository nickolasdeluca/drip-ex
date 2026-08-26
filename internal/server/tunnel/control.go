package tunnel

import (
	"errors"
	"fmt"
	"io"
	"time"

	"drip/internal/shared/protocol"
	json "github.com/goccy/go-json"
)

// ErrNoControlStream means the client cannot be told anything: it is either an
// older build that never opens a control stream, or it has not opened one yet.
var ErrNoControlStream = errors.New("this client has no control stream")

// controlWriteTimeout bounds a control write. The stream is multiplexed onto
// the same session as tunnel traffic, so a stuck write would otherwise hold the
// caller — an admin request — for as long as the client stayed wedged.
const controlWriteTimeout = 5 * time.Second

// SetControlStream attaches the stream the client opened for server-initiated
// messages. A second stream replaces the first: a reconnect is the only way a
// client gets here, and the newest one is the live one.
func (c *Connection) SetControlStream(stream io.ReadWriteCloser) {
	c.mu.Lock()
	previous := c.control
	c.control = stream
	c.mu.Unlock()

	if previous != nil && previous != stream {
		_ = previous.Close()
	}
}

// HasControlStream reports whether this tunnel can be sent control messages.
func (c *Connection) HasControlStream() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.control != nil
}

// ClearControlStream drops the stream when it goes away.
func (c *Connection) ClearControlStream(stream io.ReadWriteCloser) {
	c.mu.Lock()
	if c.control == stream {
		c.control = nil
	}
	c.mu.Unlock()
}

// Rebind asks the client to reconnect and register under subdomain, which may
// be empty to mean "ask for nothing and take what the server resolves".
//
// It returns once the message is written, not once the client has moved: the
// client acts on it by reconnecting, and this connection dies in the process.
func (c *Connection) Rebind(subdomain, reason string) error {
	c.mu.RLock()
	stream := c.control
	c.mu.RUnlock()

	if stream == nil {
		return ErrNoControlStream
	}

	payload, err := json.Marshal(protocol.RebindRequest{Subdomain: subdomain, Reason: reason})
	if err != nil {
		return fmt.Errorf("failed to encode rebind: %w", err)
	}

	if deadline, ok := stream.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = deadline.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
		defer func() { _ = deadline.SetWriteDeadline(time.Time{}) }()
	}

	if err := protocol.WriteFrame(stream, protocol.NewFrame(protocol.FrameTypeRebind, payload)); err != nil {
		return fmt.Errorf("failed to send rebind: %w", err)
	}
	return nil
}

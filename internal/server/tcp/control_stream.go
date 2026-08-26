package tcp

import (
	"net"

	"github.com/hashicorp/yamux"
	"go.uber.org/zap"
)

// acceptControlStreams takes the streams a client opens on its primary session.
//
// Data always flows the other way — the server opens a stream per request — so
// anything arriving here is the client offering a channel for server-initiated
// messages. Clients that predate the control stream never open one, and the
// loop simply ends when their session does.
func (c *Connection) acceptControlStreams(session *yamux.Session) {
	for {
		stream, err := session.Accept()
		if err != nil {
			return
		}

		if c.tunnelConn == nil {
			_ = stream.Close()
			continue
		}

		c.logger.Debug("Control stream opened",
			zap.String("subdomain", c.subdomain),
		)
		c.tunnelConn.SetControlStream(stream)
		go c.watchControlStream(stream)
	}
}

// watchControlStream drops the stream once the client stops holding it. The
// client sends nothing, so the first read only ever returns an error, and that
// error is the signal.
func (c *Connection) watchControlStream(stream net.Conn) {
	buf := make([]byte, 1)
	for {
		if _, err := stream.Read(buf); err != nil {
			if c.tunnelConn != nil {
				c.tunnelConn.ClearControlStream(stream)
			}
			_ = stream.Close()
			return
		}
	}
}

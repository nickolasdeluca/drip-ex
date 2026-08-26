package tcp

import (
	"testing"

	json "github.com/goccy/go-json"
	"go.uber.org/zap"

	"drip/internal/shared/protocol"
)

func newRebindTestClient(t *testing.T) *PoolClient {
	t.Helper()

	c := NewPoolClient(&ConnectorConfig{
		ServerAddr: "tunnel.example.com:443",
		TunnelType: protocol.TunnelTypeHTTP,
		LocalHost:  "127.0.0.1",
		LocalPort:  9765,
		Subdomain:  "wild-otter",
	}, zap.NewNop())
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func rebindFrame(t *testing.T, req protocol.RebindRequest) *protocol.Frame {
	t.Helper()

	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal rebind: %v", err)
	}
	return protocol.NewFrame(protocol.FrameTypeRebind, payload)
}

func TestRebindRecordsTheNameAndCloses(t *testing.T) {
	c := newRebindTestClient(t)

	if _, ok := c.PendingRebind(); ok {
		t.Fatal("PendingRebind() reported a request before any frame arrived")
	}

	if done := c.handleControlFrame(rebindFrame(t, protocol.RebindRequest{
		Subdomain: "billing",
		Reason:    "pinned from the admin panel",
	})); !done {
		t.Fatal("handleControlFrame() = false, want the control stream to end")
	}

	name, ok := c.PendingRebind()
	if !ok || name != "billing" {
		t.Fatalf("PendingRebind() = %q, %v, want billing, true", name, ok)
	}
	// Acting on a rebind means registering again, so the client has to drop.
	if !c.IsClosed() {
		t.Error("IsClosed() = false, want the client closed so it reconnects")
	}
}

// "Ask for nothing" is a real instruction: it lands the client on whatever
// reservation the server resolves for it, and must not read as "no request".
func TestRebindToEmptyNameIsARequest(t *testing.T) {
	c := newRebindTestClient(t)

	c.handleControlFrame(rebindFrame(t, protocol.RebindRequest{Subdomain: ""}))

	name, ok := c.PendingRebind()
	if !ok {
		t.Fatal("PendingRebind() reported no request, want one")
	}
	if name != "" {
		t.Fatalf("PendingRebind() = %q, want the empty name", name)
	}
}

func TestMalformedRebindIsIgnored(t *testing.T) {
	c := newRebindTestClient(t)

	frame := protocol.NewFrame(protocol.FrameTypeRebind, []byte("{not json"))
	if done := c.handleControlFrame(frame); done {
		t.Fatal("handleControlFrame() = true, want the stream kept open")
	}
	if _, ok := c.PendingRebind(); ok {
		t.Error("PendingRebind() reported a request from a malformed frame")
	}
	if c.IsClosed() {
		t.Error("IsClosed() = true, want the tunnel left alone")
	}
}

// An unknown control frame is something a newer server sent. Ignoring it keeps
// the tunnel up instead of dropping traffic over a message this build cannot
// read.
func TestUnknownControlFrameIsIgnored(t *testing.T) {
	c := newRebindTestClient(t)

	if done := c.handleControlFrame(protocol.NewFrame(protocol.FrameTypeHeartbeat, nil)); done {
		t.Fatal("handleControlFrame() = true, want the stream kept open")
	}
	if c.IsClosed() {
		t.Error("IsClosed() = true, want the tunnel left alone")
	}
}

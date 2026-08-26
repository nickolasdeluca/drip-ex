package tunnel

import (
	"errors"
	"net"
	"testing"
	"time"

	json "github.com/goccy/go-json"
	"go.uber.org/zap"

	"drip/internal/shared/protocol"
)

func TestRebindWithoutControlStream(t *testing.T) {
	conn := NewConnection("wild-otter", nil, zap.NewNop())

	if err := conn.Rebind("billing", "test"); !errors.Is(err, ErrNoControlStream) {
		t.Fatalf("Rebind() error = %v, want ErrNoControlStream", err)
	}
	if conn.HasControlStream() {
		t.Error("HasControlStream() = true, want false")
	}
}

func TestRebindWritesAFrame(t *testing.T) {
	conn := NewConnection("wild-otter", nil, zap.NewNop())

	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	conn.SetControlStream(server)

	if !conn.HasControlStream() {
		t.Fatal("HasControlStream() = false, want true")
	}

	done := make(chan error, 1)
	go func() { done <- conn.Rebind("billing", "pinned") }()

	if err := client.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	frame, err := protocol.ReadFrame(client)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	defer frame.Release()

	if err := <-done; err != nil {
		t.Fatalf("Rebind() error = %v", err)
	}
	if frame.Type != protocol.FrameTypeRebind {
		t.Fatalf("frame type = %s, want Rebind", frame.Type)
	}

	var req protocol.RebindRequest
	if err := json.Unmarshal(frame.Payload, &req); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if req.Subdomain != "billing" || req.Reason != "pinned" {
		t.Fatalf("payload = %+v, want billing/pinned", req)
	}
}

// A reconnect brings a new stream. The old one is dead weight and must not be
// the one a later rebind is written to.
func TestSetControlStreamReplacesThePrevious(t *testing.T) {
	conn := NewConnection("wild-otter", nil, zap.NewNop())

	oldServer, oldClient := net.Pipe()
	t.Cleanup(func() { _ = oldClient.Close() })
	conn.SetControlStream(oldServer)

	newServer, newClient := net.Pipe()
	t.Cleanup(func() { _ = newServer.Close(); _ = newClient.Close() })
	conn.SetControlStream(newServer)

	// The replaced stream is closed, so reading it fails instead of hanging.
	if _, err := oldClient.Read(make([]byte, 1)); err == nil {
		t.Fatal("read on the replaced stream succeeded, want it closed")
	}

	go func() { _ = conn.Rebind("billing", "pinned") }()

	if err := newClient.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	frame, err := protocol.ReadFrame(newClient)
	if err != nil {
		t.Fatalf("ReadFrame() on the new stream error = %v", err)
	}
	frame.Release()
}

func TestClearControlStreamOnlyClearsItsOwn(t *testing.T) {
	conn := NewConnection("wild-otter", nil, zap.NewNop())

	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	conn.SetControlStream(server)

	other, otherPeer := net.Pipe()
	t.Cleanup(func() { _ = other.Close(); _ = otherPeer.Close() })

	conn.ClearControlStream(other)
	if !conn.HasControlStream() {
		t.Fatal("ClearControlStream(other) dropped the live stream")
	}

	conn.ClearControlStream(server)
	if conn.HasControlStream() {
		t.Fatal("ClearControlStream(server) left the stream attached")
	}
}

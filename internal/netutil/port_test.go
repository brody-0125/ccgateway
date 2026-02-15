package netutil

import (
	"net"
	"testing"
)

func TestFindAvailablePort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port

	port, err := FindAvailablePort(busy, 5, nil)
	if err != nil {
		t.Fatalf("FindAvailablePort failed: %v", err)
	}
	if port == busy {
		t.Fatalf("expected non-busy port, got %d", port)
	}
}

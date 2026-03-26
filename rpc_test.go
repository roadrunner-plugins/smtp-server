package smtp

import (
	"testing"

	"go.uber.org/zap"
)

func TestListConnections_Empty(t *testing.T) {
	log, _ := zap.NewDevelopment()
	p := &Plugin{log: log}
	r := &rpc{p: p}

	var conns []ConnectionInfo
	err := r.ListConnections(false, &conns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conns) != 0 {
		t.Errorf("expected 0 connections, got %d", len(conns))
	}
}

func TestListConnections_WithSessions(t *testing.T) {
	log, _ := zap.NewDevelopment()
	p := &Plugin{log: log}
	r := &rpc{p: p}

	// Add sessions
	s1 := &Session{
		uuid:          "uuid-1",
		remoteAddr:    "10.0.0.1:1234",
		from:          "a@test.com",
		to:            []string{"b@test.com"},
		authenticated: true,
		authUsername:   "user1",
	}
	s2 := &Session{
		uuid:       "uuid-2",
		remoteAddr: "10.0.0.2:5678",
		from:       "c@test.com",
		to:         []string{"d@test.com", "e@test.com"},
	}

	p.connections.Store("uuid-1", s1)
	p.connections.Store("uuid-2", s2)

	var conns []ConnectionInfo
	err := r.ListConnections(false, &conns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conns) != 2 {
		t.Fatalf("expected 2 connections, got %d", len(conns))
	}

	// Find uuid-1
	var found bool
	for _, c := range conns {
		if c.UUID == "uuid-1" {
			found = true
			if c.RemoteAddr != "10.0.0.1:1234" {
				t.Errorf("expected remote addr 10.0.0.1:1234, got %s", c.RemoteAddr)
			}
			if !c.Authenticated {
				t.Error("expected authenticated = true")
			}
			if c.Username != "user1" {
				t.Errorf("expected username user1, got %s", c.Username)
			}
		}
	}
	if !found {
		t.Error("uuid-1 not found in connections list")
	}
}

func TestCloseConnection_NotFound(t *testing.T) {
	log, _ := zap.NewDevelopment()
	p := &Plugin{log: log}
	r := &rpc{p: p}

	var success bool
	err := r.CloseConnection("nonexistent", &success)
	if err == nil {
		t.Error("expected error for nonexistent connection")
	}
	if success {
		t.Error("success should be false")
	}
}

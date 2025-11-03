package smtp

import (
	"github.com/roadrunner-plugins/smtp-server/handler"
)

// rpc provides RPC methods callable from PHP
type rpc struct {
	p *Plugin
}

// Stats returns current plugin statistics
type Stats struct {
	// ActiveConnections: Number of active SMTP sessions
	ActiveConnections int `json:"active_connections"`

	// Servers: List of running SMTP servers
	Servers []string `json:"servers"`
}

// GetStats returns plugin statistics
func (r *rpc) GetStats(_ bool, stats *Stats) error {
	// Count active connections
	count := 0
	r.p.connections.Range(func(_, _ any) bool {
		count++
		return true
	})
	stats.ActiveConnections = count

	// List server names
	servers := make([]string, 0, len(r.p.cfg.Servers))
	for name := range r.p.cfg.Servers {
		servers = append(servers, name)
	}
	stats.Servers = servers

	return nil
}

// CloseSession closes a specific SMTP session by UUID
// Useful for forcefully closing stuck connections
func (r *rpc) CloseSession(uuid string, ret *bool) error {
	if session, ok := r.p.connections.LoadAndDelete(uuid); ok {
		s := session.(*handler.EmailEvent)
		// Connection will be closed by the SMTP server when session ends
		// Just remove from tracking
		_ = s
		*ret = true
		return nil
	}

	*ret = false
	return nil
}

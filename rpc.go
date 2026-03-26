package smtp

import (
	"github.com/roadrunner-server/errors"
)

// ConnectionInfo represents information about an active SMTP connection
type ConnectionInfo struct {
	UUID          string   `json:"uuid"`
	RemoteAddr    string   `json:"remote_addr"`
	From          string   `json:"from"`
	To            []string `json:"to"`
	Authenticated bool     `json:"authenticated"`
	Username      string   `json:"username"`
}

// rpc provides RPC interface for external management
type rpc struct {
	p *Plugin
}

// CloseConnection closes SMTP connection by UUID
func (r *rpc) CloseConnection(uuid string, success *bool) error {
	*success = false

	value, ok := r.p.connections.Load(uuid)
	if !ok {
		return errors.Str("connection not found")
	}

	session := value.(*Session)

	// Mark session as closing (Logout will handle map cleanup)
	session.shouldClose = true

	// Close underlying connection — triggers Logout() which deletes from map
	if session.conn != nil && session.conn.Conn() != nil {
		_ = session.conn.Conn().Close()
	}
	*success = true

	return nil
}

// ListConnections returns active SMTP connections
func (r *rpc) ListConnections(_ bool, connections *[]ConnectionInfo) error {
	result := make([]ConnectionInfo, 0)

	r.p.connections.Range(func(key, value any) bool {
		session := value.(*Session)
		result = append(result, ConnectionInfo{
			UUID:          session.uuid,
			RemoteAddr:    session.remoteAddr,
			From:          session.from,
			To:            session.to,
			Authenticated: session.authenticated,
			Username:      session.authUsername,
		})
		return true
	})

	*connections = result
	return nil
}

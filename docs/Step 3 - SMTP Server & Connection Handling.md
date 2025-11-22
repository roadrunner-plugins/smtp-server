## Goal

Implement SMTP server using `emersion/go-smtp` library, handle incoming connections, and process basic SMTP protocol commands. After this step, external email clients can connect and the server will log SMTP transactions (but won't send to PHP workers yet).

---

## Components to Implement

### 3.1 SMTP Library Integration

**Chosen Library**: `github.com/emersion/go-smtp`

**Why**:

- ✅ Production-ready, RFC 5321 compliant
- ✅ Handles low-level protocol details
- ✅ Extensible backend interface for custom logic
- ✅ Built-in authentication support (we'll capture but not verify)
- ✅ Active maintenance, used in production projects

### 3.2 Extended Plugin Structure

```go
package smtp

import (
    "sync"
    "github.com/emersion/go-smtp"
)

type Plugin struct {
    mu     sync.RWMutex
    cfg    *Config
    log    *zap.Logger
    server Server
    pool   Pool
    
    // SMTP server components
    smtpServer *smtp.Server          // SMTP server instance
    listener   net.Listener          // Network listener
    connections sync.Map             // uuid -> connection metadata
}
```

### 3.3 SMTP Backend Interface

The `emersion/go-smtp` library requires implementing a Backend interface:

```go
// Backend implements go-smtp backend interface
type Backend struct {
    plugin *Plugin
    log    *zap.Logger
}

// NewBackend creates SMTP backend
func NewBackend(plugin *Plugin) *Backend {
    return &Backend{
        plugin: plugin,
        log:    plugin.log,
    }
}

// Methods required by go-smtp:

// NewSession is called when new SMTP connection is established
func (b *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
    session := &Session{
        backend:    b,
        conn:       c,
        uuid:       uuid.NewString(),
        remoteAddr: c.Conn().RemoteAddr().String(),
        log:        b.log,
    }
    
    // Store connection for management
    b.plugin.connections.Store(session.uuid, session)
    
    b.log.Debug("new SMTP connection",
        zap.String("uuid", session.uuid),
        zap.String("remote_addr", session.remoteAddr),
    )
    
    return session, nil
}

// AnonymousLogin is called for unauthenticated connections
// We allow all connections (profiling mode)
func (b *Backend) Login(state *smtp.ConnectionState, username, password string) (smtp.Session, error) {
    session := &Session{
        backend:    b,
        conn:       state.Conn,
        uuid:       uuid.NewString(),
        remoteAddr: state.RemoteAddr.String(),
        log:        b.log,
        
        // Capture auth credentials
        authenticated: true,
        authUsername:  username,
        authPassword:  password,
    }
    
    b.plugin.connections.Store(session.uuid, session)
    
    b.log.Debug("SMTP authentication attempt",
        zap.String("uuid", session.uuid),
        zap.String("username", username),
    )
    
    return session, nil
}
```

### 3.4 SMTP Session Implementation

```go
// Session represents an SMTP session (one connection)
type Session struct {
    backend    *Backend
    conn       *smtp.Conn
    uuid       string
    remoteAddr string
    log        *zap.Logger
    
    // Authentication data (captured but not verified)
    authenticated bool
    authUsername  string
    authPassword  string
    authMechanism string
    
    // SMTP envelope data
    from       string
    to         []string
    heloName   string
    
    // Email data (accumulated during DATA command)
    emailData  bytes.Buffer
}

// Methods required by go-smtp Session interface:

// Mail is called for MAIL FROM command
func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
    s.from = from
    s.log.Debug("MAIL FROM",
        zap.String("uuid", s.uuid),
        zap.String("from", from),
    )
    return nil
}

// Rcpt is called for RCPT TO command
func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
    s.to = append(s.to, to)
    s.log.Debug("RCPT TO",
        zap.String("uuid", s.uuid),
        zap.String("to", to),
    )
    return nil
}

// Data is called when DATA command is received
// Returns io.WriteCloser where email content will be written
func (s *Session) Data(r io.Reader) error {
    s.log.Debug("DATA command received", zap.String("uuid", s.uuid))
    
    // Read email data into buffer
    s.emailData.Reset()
    _, err := io.Copy(&s.emailData, r)
    if err != nil {
        s.log.Error("failed to read email data", zap.Error(err))
        return err
    }
    
    s.log.Info("email received",
        zap.String("uuid", s.uuid),
        zap.String("from", s.from),
        zap.Strings("to", s.to),
        zap.Int("size", s.emailData.Len()),
    )
    
    // Step 4 will send this data to PHP workers
    // For now, just log it
    
    return nil
}

// Reset is called for RSET command
func (s *Session) Reset() {
    s.from = ""
    s.to = nil
    s.emailData.Reset()
    s.log.Debug("session reset", zap.String("uuid", s.uuid))
}

// Logout is called when connection closes
func (s *Session) Logout() error {
    s.log.Debug("connection closed", zap.String("uuid", s.uuid))
    s.backend.plugin.connections.Delete(s.uuid)
    return nil
}
```

### 3.5 Updated Serve Method

```go
// Serve starts SMTP server and worker pool
func (p *Plugin) Serve() chan error {
    errCh := make(chan error, 1)
    
    p.mu.Lock()
    defer p.mu.Unlock()
    
    // 1. Create worker pool
    pool, err := p.server.NewPool(
        context.Background(),
        p.cfg.Pool,
        map[string]string{"RR_MODE": "smtp"},
        p.log,
    )
    if err != nil {
        errCh <- errors.E("smtp_serve", err)
        return errCh
    }
    p.pool = pool
    
    // 2. Create SMTP backend
    backend := NewBackend(p)
    
    // 3. Create SMTP server
    p.smtpServer = smtp.NewServer(backend)
    
    // Configure server from config
    p.smtpServer.Addr = p.cfg.Addr
    p.smtpServer.Domain = p.cfg.Hostname
    p.smtpServer.ReadTimeout = p.cfg.ReadTimeout
    p.smtpServer.WriteTimeout = p.cfg.WriteTimeout
    p.smtpServer.MaxMessageBytes = int(p.cfg.MaxMessageSize)
    p.smtpServer.MaxRecipients = 100  // Reasonable limit
    p.smtpServer.AllowInsecureAuth = true  // Allow PLAIN auth over non-TLS
    
    p.log.Info("SMTP server configured",
        zap.String("addr", p.smtpServer.Addr),
        zap.String("domain", p.smtpServer.Domain),
    )
    
    // 4. Create listener
    listener, err := net.Listen("tcp", p.cfg.Addr)
    if err != nil {
        errCh <- errors.E("smtp_listen", err)
        return errCh
    }
    p.listener = listener
    
    // 5. Start SMTP server in goroutine
    go func() {
        p.log.Info("SMTP server starting", zap.String("addr", p.cfg.Addr))
        
        if err := p.smtpServer.Serve(listener); err != nil {
            p.log.Error("SMTP server error", zap.Error(err))
            errCh <- err
        }
    }()
    
    return errCh
}
```

### 3.6 Updated Stop Method

```go
// Stop gracefully stops SMTP server and workers
func (p *Plugin) Stop(ctx context.Context) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    p.log.Info("stopping SMTP plugin")
    
    doneCh := make(chan struct{}, 1)
    
    go func() {
        // 1. Close listener (stops accepting new connections)
        if p.listener != nil {
            _ = p.listener.Close()
        }
        
        // 2. Close SMTP server (graceful, waits for active connections)
        if p.smtpServer != nil {
            _ = p.smtpServer.Close()
        }
        
        // 3. Close all tracked connections
        p.connections.Range(func(key, value any) bool {
            // Sessions will be cleaned up by Logout()
            return true
        })
        
        // 4. Destroy worker pool
        if p.pool != nil {
            p.pool.Destroy(ctx)
        }
        
        doneCh <- struct{}{}
    }()
    
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-doneCh:
        p.log.Info("SMTP plugin stopped")
        return nil
    }
}
```

### 3.7 RPC Extension - Close Connection

```go
// In rpc.go

// CloseConnection closes SMTP connection by UUID
func (r *rpc) CloseConnection(uuid string, success *bool) error {
    *success = false
    
    value, ok := r.p.connections.Load(uuid)
    if !ok {
        return errors.Str("connection not found")
    }
    
    session := value.(*Session)
    
    // Close underlying connection
    if session.conn != nil && session.conn.Conn() != nil {
        _ = session.conn.Conn().Close()
    }
    
    r.p.connections.Delete(uuid)
    *success = true
    
    return nil
}

// ListConnections returns active SMTP connections
func (r *rpc) ListConnections(_ bool, connections *[]ConnectionInfo) error {
    result := make([]ConnectionInfo, 0)
    
    r.p.connections.Range(func(key, value any) bool {
        session := value.(*Session)
        result = append(result, ConnectionInfo{
            UUID:       session.uuid,
            RemoteAddr: session.remoteAddr,
            From:       session.from,
            To:         session.to,
            Authenticated: session.authenticated,
            Username:   session.authUsername,
        })
        return true
    })
    
    *connections = result
    return nil
}

type ConnectionInfo struct {
    UUID          string   `json:"uuid"`
    RemoteAddr    string   `json:"remote_addr"`
    From          string   `json:"from"`
    To            []string `json:"to"`
    Authenticated bool     `json:"authenticated"`
    Username      string   `json:"username"`
}
```

---

## File Structure Update

```
smtp/
├── go.mod                # Updated: add emersion/go-smtp
├── config.go
├── plugin.go             # Updated: Serve with SMTP server, Stop
├── backend.go            # NEW: SMTP Backend implementation
├── session.go            # NEW: SMTP Session implementation
├── rpc.go                # Updated: CloseConnection, ListConnections
├── workers_manager.go
└── .plugin.yaml
```

---

## Dependencies Update (go.mod)

```go
module github.com/buggregator/smtp-server

go 1.23

require (
    github.com/emersion/go-smtp v0.21.3      // NEW: SMTP server library
    github.com/google/uuid v1.6.0            // NEW: UUID generation
    github.com/roadrunner-server/errors v1.4.1
    github.com/roadrunner-server/pool v1.1.3
    github.com/roadrunner-server/api/v4 v4.x.x
    go.uber.org/zap v1.27.0
)
```

---

## Verification

After this step:

1. ✅ SMTP server listens on configured address (`127.0.0.1:1025`)
2. ✅ Can connect with email client (telnet, Thunderbird, etc.)
3. ✅ SMTP commands work:
    - `HELO/EHLO client.example.com`
    - `AUTH LOGIN` (accepts any credentials)
    - `MAIL FROM:<sender@example.com>`
    - `RCPT TO:<recipient@example.com>`
    - `DATA` + email content + `.`
    - `QUIT`
4. ✅ Server responds with proper SMTP codes (250 OK, etc.)
5. ✅ Logs show connection lifecycle (connect, commands, email received, disconnect)
6. ✅ Multiple concurrent connections work
7. ✅ RPC commands:
    - `rr rpc smtp.ListConnections` - shows active connections
    - `rr rpc smtp.CloseConnection <uuid>` - closes connection
8. ✅ Graceful shutdown closes active connections

**What's NOT working yet**:

- ❌ Email data not sent to PHP workers (logged only)
- ❌ No email parsing (headers, body, attachments)

---

## Testing with Telnet

```bash
# Connect to SMTP server
telnet localhost 1025

# SMTP session:
> 220 buggregator.local ESMTP
EHLO client.example.com
> 250-buggregator.local
> 250 AUTH PLAIN LOGIN
AUTH LOGIN
> 334 VXNlcm5hbWU6
dGVzdHVzZXI=       # base64("testuser")
> 334 UGFzc3dvcmQ6
dGVzdHBhc3M=       # base64("testpass")
> 235 Authentication successful
MAIL FROM:<sender@example.com>
> 250 OK
RCPT TO:<recipient@example.com>
> 250 OK
DATA
> 354 Start mail input
Subject: Test Email

This is test email body.
.
> 250 OK: message accepted
QUIT
> 221 Bye
```

---

## Testing with Go Client

```go
package main

import (
    "log"
    "net/smtp"
)

func main() {
    // Connect to SMTP server
    c, err := smtp.Dial("127.0.0.1:1025")
    if err != nil {
        log.Fatal(err)
    }
    defer c.Close()
    
    // Set sender
    if err := c.Mail("sender@example.com"); err != nil {
        log.Fatal(err)
    }
    
    // Set recipient
    if err := c.Rcpt("recipient@example.com"); err != nil {
        log.Fatal(err)
    }
    
    // Send email data
    w, err := c.Data()
    if err != nil {
        log.Fatal(err)
    }
    
    _, err = w.Write([]byte("Subject: Test\r\n\r\nTest email body"))
    if err != nil {
        log.Fatal(err)
    }
    
    err = w.Close()
    if err != nil {
        log.Fatal(err)
    }
    
    c.Quit()
    log.Println("Email sent successfully!")
}
```

---

## Next Step Preview

**Step 4** will implement email parsing (MIME multipart, headers, attachments) and sending complete email data to PHP workers as JSON events.
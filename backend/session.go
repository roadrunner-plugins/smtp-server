package backend

import (
	"bytes"
	"io"
	"sync"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/roadrunner-server/pool/payload"
	"github.com/roadrunner-server/smtp/v5/handler"
	"go.uber.org/zap"
)

// Session implements smtp.Session interface for handling a single SMTP connection
// Each connection gets its own session instance with isolated state
type Session struct {
	// Unique identifier for this email transaction
	uuid string
	
	// Server name from configuration
	serverName string
	
	// Client connection info
	conn *smtp.Conn
	
	// Configuration limits
	maxMessageSize int64
	maxRecipients  int
	includeRaw     bool
	
	// Attachment handling
	attachmentMode string
	tempDir        string
	
	// SMTP transaction state
	from       string   // MAIL FROM
	to         []string // RCPT TO addresses
	helo       string   // HELO/EHLO hostname
	authAttempted bool
	authMechanism string
	authUsername  string
	authPassword  string
	
	// PHP worker pool executor
	phpExec func(*payload.Payload) (*payload.Payload, error)
	
	// Connection tracking
	connections *sync.Map
	
	// Resource pools
	emailEventPool *sync.Pool
	payloadPool    *sync.Pool
	bufferPool     *sync.Pool
	
	// Logger
	log *zap.Logger
}

// NewSession creates a new SMTP session instance
func NewSession(
	c *smtp.Conn,
	serverName string,
	maxMessageSize int64,
	maxRecipients int,
	includeRaw bool,
	attachmentMode string,
	tempDir string,
	phpExec func(*payload.Payload) (*payload.Payload, error),
	connections *sync.Map,
	emailEventPool *sync.Pool,
	payloadPool *sync.Pool,
	bufferPool *sync.Pool,
	log *zap.Logger,
) *Session {
	sid := uuid.NewString()
	
	s := &Session{
		uuid:           sid,
		serverName:     serverName,
		conn:           c,
		maxMessageSize: maxMessageSize,
		maxRecipients:  maxRecipients,
		includeRaw:     includeRaw,
		attachmentMode: attachmentMode,
		tempDir:        tempDir,
		phpExec:        phpExec,
		connections:    connections,
		emailEventPool: emailEventPool,
		payloadPool:    payloadPool,
		bufferPool:     bufferPool,
		log:            log,
	}
	
	// Track this session for graceful shutdown
	connections.Store(sid, s)
	
	return s
}

// AuthPlain handles PLAIN authentication mechanism
func (s *Session) AuthPlain(username, password string) error {
	s.authAttempted = true
	s.authMechanism = "PLAIN"
	s.authUsername = username
	s.authPassword = password
	
	s.log.Debug("auth PLAIN captured",
		zap.String("username", username),
		zap.String("uuid", s.uuid),
	)
	
	// Always accept in profiler mode
	return nil
}

// Mail handles MAIL FROM command
func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	s.log.Debug("MAIL FROM", zap.String("from", from), zap.String("uuid", s.uuid))
	return nil
}

// Rcpt handles RCPT TO command
func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	if len(s.to) >= s.maxRecipients {
		return smtp.SMTPError{
			Code:    452,
			Message: "Too many recipients",
		}
	}
	
	s.to = append(s.to, to)
	s.log.Debug("RCPT TO", zap.String("to", to), zap.String("uuid", s.uuid))
	return nil
}

// Data handles DATA command - receives email body and forwards to PHP
func (s *Session) Data(r io.Reader) error {
	s.log.Debug("DATA command received", zap.String("uuid", s.uuid))
	
	// Read email body with size limit
	buf := s.bufferPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		s.bufferPool.Put(buf)
	}()
	
	limitedReader := io.LimitReader(r, s.maxMessageSize)
	n, err := buf.ReadFrom(limitedReader)
	if err != nil {
		s.log.Error("failed to read email body", zap.Error(err))
		return err
	}
	
	if n >= s.maxMessageSize {
		return smtp.SMTPError{
			Code:    552,
			Message: "Message too large",
		}
	}
	
	rawMessage := buf.Bytes()
	
	// Parse email and extract attachments
	emailEvent, err := ParseEmail(
		rawMessage,
		s.uuid,
		s.serverName,
		s.conn.Conn().RemoteAddr().String(),
		s.from,
		s.to,
		s.helo,
		s.authAttempted,
		s.authMechanism,
		s.authUsername,
		s.authPassword,
		s.includeRaw,
		s.attachmentMode,
		s.tempDir,
		s.log,
	)
	if err != nil {
		s.log.Error("failed to parse email", zap.Error(err))
		return err
	}
	
	// Marshal to JSON
	jsonData, err := json.Marshal(emailEvent)
	if err != nil {
		s.log.Error("failed to marshal email event", zap.Error(err))
		return err
	}
	
	// Send to PHP worker
	pld := s.payloadPool.Get().(*payload.Payload)
	pld.Context = jsonData
	pld.Body = nil // Email content already in Context
	
	rsp, err := s.phpExec(pld)
	if err != nil {
		s.log.Error("PHP worker execution failed", zap.Error(err))
		s.payloadPool.Put(pld)
		
		// Return temporary error to SMTP client
		return smtp.SMTPError{
			Code:    451,
			Message: "Temporary failure processing email",
		}
	}
	
	s.payloadPool.Put(pld)
	
	// Check PHP response
	if bytes.Equal(rsp.Context, handler.CLOSE) {
		s.log.Debug("PHP requested connection close", zap.String("uuid", s.uuid))
		return smtp.SMTPError{
			Code:    421,
			Message: "Service closing connection",
		}
	}
	
	// Default: accept email and continue (CONTINUE or any other response)
	s.log.Info("email accepted",
		zap.String("uuid", s.uuid),
		zap.String("from", s.from),
		zap.Strings("to", s.to),
	)
	
	return nil
}

// Reset handles RSET command - resets transaction state
func (s *Session) Reset() {
	s.from = ""
	s.to = nil
	s.authAttempted = false
	s.authMechanism = ""
	s.authUsername = ""
	s.authPassword = ""
	
	s.log.Debug("session reset", zap.String("uuid", s.uuid))
}

// Logout handles connection close
func (s *Session) Logout() error {
	s.connections.Delete(s.uuid)
	s.log.Debug("session logout", zap.String("uuid", s.uuid))
	return nil
}

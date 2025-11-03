package backend

import (
	"sync"

	"github.com/emersion/go-smtp"
	"github.com/roadrunner-server/pool/payload"
	"go.uber.org/zap"
)

// Backend implements smtp.Backend interface from github.com/emersion/go-smtp
// It's responsible for creating new SMTP sessions for each connection
type Backend struct {
	serverName string
	hostname   string

	// Configuration
	maxMessageSize int64
	maxRecipients  int
	includeRaw     bool

	// Attachment handling
	attachmentMode string
	tempDir        string

	// PHP worker pool executor
	phpExec func(*payload.Payload) (*payload.Payload, error)

	// Connection tracking for graceful shutdown
	connections *sync.Map // uuid -> *Session

	// Resource pools
	emailEventPool *sync.Pool
	payloadPool    *sync.Pool
	bufferPool     *sync.Pool

	// Logger
	log *zap.Logger
}

// NewBackend creates a new SMTP backend for a specific server configuration
func NewBackend(
	serverName string,
	hostname string,
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
) *Backend {
	return &Backend{
		serverName:     serverName,
		hostname:       hostname,
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
}

// NewSession creates a new SMTP session for an incoming connection
// Called by go-smtp library for each new connection
func (b *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return NewSession(
		c,
		b.serverName,
		b.maxMessageSize,
		b.maxRecipients,
		b.includeRaw,
		b.attachmentMode,
		b.tempDir,
		b.phpExec,
		b.connections,
		b.emailEventPool,
		b.payloadPool,
		b.bufferPool,
		b.log,
	), nil
}

// AnonymousLogin handles connections without authentication
// Always accepts in profiler mode
func (b *Backend) AnonymousLogin(c *smtp.Conn) (smtp.Session, error) {
	return b.NewSession(c)
}

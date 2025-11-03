package smtp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/goridge/v3/pkg/frame"
	"github.com/roadrunner-server/pool/payload"
	"github.com/roadrunner-server/pool/pool"
	staticPool "github.com/roadrunner-server/pool/pool/static_pool"
	"github.com/roadrunner-server/pool/state/process"
	"github.com/roadrunner-server/pool/worker"
	"github.com/roadrunner-server/smtp/v5/backend"
	"github.com/roadrunner-server/smtp/v5/handler"
	"go.uber.org/zap"
)

const (
	pluginName string = "smtp"
	RrMode     string = "RR_MODE"
)

// Pool interface for PHP worker pool operations
type Pool interface {
	Workers() []*worker.Process
	RemoveWorker(ctx context.Context) error
	AddWorker() error
	Exec(ctx context.Context, p *payload.Payload, stopCh chan struct{}) (chan *staticPool.PExec, error)
	Reset(ctx context.Context) error
	Destroy(ctx context.Context)
}

// Logger provides named logger instances
type Logger interface {
	NamedLogger(name string) *zap.Logger
}

// Server creates PHP worker pools
type Server interface {
	NewPool(ctx context.Context, cfg *pool.Config, env map[string]string, _ *zap.Logger) (*staticPool.Pool, error)
}

// Configurer reads plugin configuration
type Configurer interface {
	UnmarshalKey(name string, out any) error
	Has(name string) bool
}

// Plugin is the main SMTP plugin implementation
type Plugin struct {
	mu  sync.RWMutex
	cfg *Config
	log *zap.Logger
	
	// PHP worker pool
	server Server
	wPool  Pool
	
	// SMTP servers (one per configuration)
	smtpServers sync.Map // serverName -> *smtp.Server
	
	// Connection tracking for graceful shutdown
	connections sync.Map // uuid -> *backend.Session
	
	// Resource pools for performance
	emailEventPool sync.Pool
	payloadPool    sync.Pool
	bufferPool     sync.Pool
	
	// Temp file cleanup
	cleanupTicker  *time.Ticker
	cleanupStop    chan struct{}
}

// Init initializes the plugin with configuration
func (p *Plugin) Init(log Logger, cfg Configurer, server Server) error {
	const op = errors.Op("smtp_plugin_init")
	
	if !cfg.Has(pluginName) {
		return errors.E(op, errors.Disabled)
	}
	
	// Parse configuration
	p.cfg = &Config{}
	err := cfg.UnmarshalKey(pluginName, p.cfg)
	if err != nil {
		return errors.E(op, err)
	}
	
	err = p.cfg.InitDefault()
	if err != nil {
		return errors.E(op, err)
	}
	
	// Initialize resource pools
	p.emailEventPool = sync.Pool{
		New: func() any {
			return &handler.EmailEvent{
				Envelope: handler.Envelope{
					To: make([]string, 0, 10),
				},
				Message: handler.Message{
					Headers: make(map[string]string, 20),
				},
				Attachments: make([]handler.Attachment, 0, 5),
			}
		},
	}
	
	p.payloadPool = sync.Pool{
		New: func() any {
			return &payload.Payload{}
		},
	}
	
	p.bufferPool = sync.Pool{
		New: func() any {
			buf := new(bytes.Buffer)
			buf.Grow(10 * 1024 * 1024) // 10MB buffer for emails
			return buf
		},
	}
	
	p.log = log.NamedLogger(pluginName)
	p.server = server
	
	return nil
}

// Serve starts the SMTP servers
func (p *Plugin) Serve() chan error {
	errCh := make(chan error, 1)
	
	// Create PHP worker pool
	wPool, err := p.server.NewPool(context.Background(), p.cfg.Pool, map[string]string{RrMode: pluginName}, nil)
	if err != nil {
		errCh <- err
		return errCh
	}
	p.wPool = wPool
	
	// Start temp file cleanup if using tempfile mode
	if p.cfg.AttachmentStorage.Mode == "tempfile" {
		p.startTempFileCleanup()
	}
	
	// Start SMTP server for each configured server
	for serverName, serverCfg := range p.cfg.Servers {
		go p.serveServer(serverName, serverCfg, errCh)
	}
	
	return errCh
}

// serveServer starts a single SMTP server instance
func (p *Plugin) serveServer(serverName string, cfg *ServerConfig, errCh chan error) {
	// Create SMTP backend
	be := backend.NewBackend(
		serverName,
		cfg.Hostname,
		cfg.MaxMessageSize,
		cfg.MaxRecipients,
		p.cfg.IncludeRaw,
		p.cfg.AttachmentStorage.Mode,
		p.cfg.AttachmentStorage.TempDir,
		p.Exec,
		&p.connections,
		&p.emailEventPool,
		&p.payloadPool,
		&p.bufferPool,
		p.log,
	)
	
	// Create SMTP server
	s := smtp.NewServer(be)
	s.Addr = cfg.Addr
	s.Domain = cfg.Hostname
	s.ReadTimeout = cfg.ReadTimeout
	s.WriteTimeout = cfg.WriteTimeout
	s.MaxMessageBytes = int(cfg.MaxMessageSize)
	s.MaxRecipients = cfg.MaxRecipients
	s.AllowInsecureAuth = true // Dev tool - allow plaintext auth
	
	p.smtpServers.Store(serverName, s)
	
	p.log.Info("starting SMTP server",
		zap.String("server", serverName),
		zap.String("addr", cfg.Addr),
		zap.String("hostname", cfg.Hostname),
	)
	
	// Start listening (blocking)
	err := s.ListenAndServe()
	if err != nil {
		p.log.Error("SMTP server stopped", zap.String("server", serverName), zap.Error(err))
		errCh <- err
	}
}

// Stop performs graceful shutdown
func (p *Plugin) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	doneCh := make(chan struct{}, 1)
	
	go func() {
		// Stop temp file cleanup
		if p.cleanupStop != nil {
			close(p.cleanupStop)
			if p.cleanupTicker != nil {
				p.cleanupTicker.Stop()
			}
		}
		
		// Close all SMTP servers
		p.smtpServers.Range(func(key, value any) bool {
			srv := value.(*smtp.Server)
			err := srv.Close()
			if err != nil {
				p.log.Error("failed to close SMTP server", zap.String("server", key.(string)), zap.Error(err))
			}
			return true
		})
		
		// Destroy worker pool
		if p.wPool != nil {
			p.wPool.Destroy(ctx)
		}
		
		doneCh <- struct{}{}
	}()
	
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-doneCh:
		p.log.Info("SMTP plugin stopped gracefully")
		return nil
	}
}

// Reset resets the PHP worker pool
func (p *Plugin) Reset() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	const op = errors.Op("smtp_reset")
	p.log.Info("reset signal received")
	
	err := p.wPool.Reset(context.Background())
	if err != nil {
		return errors.E(op, err)
	}
	
	p.log.Info("plugin was successfully reset")
	return nil
}

// Workers returns current worker states
func (p *Plugin) Workers() []*process.State {
	p.mu.RLock()
	wrk := p.wPool.Workers()
	p.mu.RUnlock()
	
	ps := make([]*process.State, len(wrk))
	
	for i := range wrk {
		st, err := process.WorkerProcessState(wrk[i])
		if err != nil {
			p.log.Error("failed to get worker state", zap.Error(err))
			return nil
		}
		ps[i] = st
	}
	
	return ps
}

// Name returns plugin name
func (p *Plugin) Name() string {
	return pluginName
}

// RPC returns RPC interface
func (p *Plugin) RPC() any {
	return &rpc{p: p}
}

// Exec sends payload to PHP worker pool
func (p *Plugin) Exec(pld *payload.Payload) (*payload.Payload, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	result, err := p.wPool.Exec(context.Background(), pld, nil)
	if err != nil {
		return nil, err
	}
	
	select {
	case pldResult := <-result:
		if pldResult.Error() != nil {
			return nil, pldResult.Error()
		}
		
		// Check for streaming (not supported)
		if pldResult.Payload().Flags&frame.STREAM != 0 {
			return nil, errors.Str("streaming is not supported")
		}
		
		return pldResult.Payload(), nil
		
	default:
		return nil, errors.Str("worker empty response")
	}
}

// startTempFileCleanup starts periodic cleanup of old temp files
func (p *Plugin) startTempFileCleanup() {
	p.cleanupStop = make(chan struct{})
	p.cleanupTicker = time.NewTicker(p.cfg.AttachmentStorage.CleanupAfter / 2)
	
	go func() {
		for {
			select {
			case <-p.cleanupTicker.C:
				p.cleanupTempFiles()
			case <-p.cleanupStop:
				return
			}
		}
	}()
}

// cleanupTempFiles removes old temporary attachment files
func (p *Plugin) cleanupTempFiles() {
	tempDir := p.cfg.AttachmentStorage.TempDir
	maxAge := p.cfg.AttachmentStorage.CleanupAfter
	
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		if !os.IsNotExist(err) {
			p.log.Error("failed to read temp dir", zap.Error(err))
		}
		return
	}
	
	now := time.Now()
	cleaned := 0
	
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		
		info, err := entry.Info()
		if err != nil {
			continue
		}
		
		if now.Sub(info.ModTime()) > maxAge {
			path := fmt.Sprintf("%s/%s", tempDir, entry.Name())
			err := os.Remove(path)
			if err != nil {
				p.log.Warn("failed to remove temp file", zap.String("path", path), zap.Error(err))
			} else {
				cleaned++
			}
		}
	}
	
	if cleaned > 0 {
		p.log.Debug("cleaned temp attachment files", zap.Int("count", cleaned))
	}
}

package smtp

import (
	"context"
	"net"
	"sync"

	"github.com/emersion/go-smtp"
	"github.com/roadrunner-server/endure/v2/dep"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"
)

const (
	PluginName = "smtp"
)

// Logger interface for dependency injection
type Logger interface {
	NamedLogger(name string) *zap.Logger
}

// Configurer interface for configuration access
type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct
	UnmarshalKey(name string, out any) error
	// Has checks if a config section exists
	Has(name string) bool
}

// Plugin is the SMTP server plugin
type Plugin struct {
	mu          sync.RWMutex
	cfg         *Config
	log         *zap.Logger
	connections sync.Map // uuid -> *Session

	// Jobs plugin reference
	jobs Jobs

	// SMTP server components
	smtpServer *smtp.Server
	listener   net.Listener

	// Cleanup routine cancellation
	cleanupCancel context.CancelFunc
}

// Init initializes the plugin with configuration and logger
func (p *Plugin) Init(log Logger, cfg Configurer) error {
	const op = errors.Op("smtp_plugin_init")

	// Check if plugin is enabled
	if !cfg.Has(PluginName) {
		return errors.E(op, errors.Disabled)
	}

	// Parse configuration
	err := cfg.UnmarshalKey(PluginName, &p.cfg)
	if err != nil {
		return errors.E(op, err)
	}

	// Initialize defaults
	if err := p.cfg.InitDefaults(); err != nil {
		return errors.E(op, err)
	}

	// Setup logger
	p.log = log.NamedLogger(PluginName)

	p.log.Info("SMTP plugin initialized",
		zap.String("addr", p.cfg.Addr),
		zap.String("hostname", p.cfg.Hostname),
		zap.Int64("max_message_size", p.cfg.MaxMessageSize),
		zap.String("jobs_pipeline", p.cfg.Jobs.Pipeline),
	)

	return nil
}

// Serve starts the SMTP server
func (p *Plugin) Serve() chan error {
	errCh := make(chan error, 2)

	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if jobs plugin was collected
	if p.jobs == nil {
		errCh <- errors.E(errors.Op("smtp_serve"), errors.Str("jobs plugin not available - ensure jobs plugin is enabled and loaded"))
		return errCh
	}

	// 1. Create SMTP backend
	backend := NewBackend(p)

	// 2. Create SMTP server
	p.smtpServer = smtp.NewServer(backend)
	p.smtpServer.Addr = p.cfg.Addr
	p.smtpServer.Domain = p.cfg.Hostname
	p.smtpServer.ReadTimeout = p.cfg.ReadTimeout
	p.smtpServer.WriteTimeout = p.cfg.WriteTimeout
	p.smtpServer.MaxMessageBytes = p.cfg.MaxMessageSize
	p.smtpServer.MaxRecipients = 100
	p.smtpServer.AllowInsecureAuth = true

	p.log.Info("SMTP server configured",
		zap.String("addr", p.smtpServer.Addr),
		zap.String("domain", p.smtpServer.Domain),
		zap.String("jobs_pipeline", p.cfg.Jobs.Pipeline),
	)

	// 3. Create listener
	var err error
	p.listener, err = net.Listen("tcp", p.cfg.Addr)
	if err != nil {
		errCh <- errors.E(errors.Op("smtp_listen"), err)
		return errCh
	}

	p.log.Info("SMTP listener created", zap.String("addr", p.cfg.Addr))

	// 4. Start SMTP server in goroutine
	go func() {
		p.log.Info("SMTP server starting", zap.String("addr", p.cfg.Addr))
		if err := p.smtpServer.Serve(p.listener); err != nil {
			p.log.Error("SMTP server error", zap.Error(err))
			errCh <- err
		}
	}()

	// 5. Start temp file cleanup routine
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	p.cleanupCancel = cleanupCancel
	p.startCleanupRoutine(cleanupCtx)

	return errCh
}

// Stop gracefully stops the plugin
func (p *Plugin) Stop(ctx context.Context) error {
	p.log.Info("stopping SMTP plugin")

	doneCh := make(chan struct{}, 1)

	go func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		// 1. Stop cleanup routine
		if p.cleanupCancel != nil {
			p.cleanupCancel()
		}

		// 2. Close listener (stops accepting new connections)
		if p.listener != nil {
			_ = p.listener.Close()
		}

		// 3. Close SMTP server
		if p.smtpServer != nil {
			_ = p.smtpServer.Close()
		}

		// 4. Close all tracked connections
		p.connections.Range(func(key, value any) bool {
			session := value.(*Session)
			if session.conn != nil && session.conn.Conn() != nil {
				_ = session.conn.Conn().Close()
			}
			p.connections.Delete(key)
			return true
		})

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

// Name returns plugin name for RoadRunner
func (p *Plugin) Name() string {
	return PluginName
}

// Collects declares dependencies on other plugins
func (p *Plugin) Collects() []*dep.In {
	return []*dep.In{
		dep.Fits(func(pp any) {
			// Collect Jobs plugin that implements Push method
			p.jobs = pp.(Jobs)
			p.log.Debug("collected jobs plugin")
		}, (*Jobs)(nil)),
	}
}

// RPC returns RPC interface for external management
func (p *Plugin) RPC() any {
	return &rpc{p: p}
}

// pushToJobs sends email as job to Jobs plugin
func (p *Plugin) pushToJobs(email *EmailData) error {
	const op = errors.Op("smtp_push_to_jobs")

	if p.jobs == nil {
		return errors.E(op, errors.Str("jobs plugin not available - ensure jobs plugin is enabled and loaded before smtp plugin"))
	}

	// Convert to domain model
	msg, err := emailToJobMessage(email, &p.cfg.Jobs)
	if err != nil {
		return errors.E(op, err)
	}

	// Push directly to Jobs plugin
	err = p.jobs.Push(context.Background(), msg)
	if err != nil {
		return errors.E(op, err)
	}

	p.log.Debug("email pushed to jobs",
		zap.String("uuid", email.UUID),
		zap.String("pipeline", p.cfg.Jobs.Pipeline),
	)

	return nil
}

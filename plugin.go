package smtp

import (
	"context"
	"net"
	"sync"

	"github.com/emersion/go-smtp"
	jobsProto "github.com/roadrunner-server/api/v4/build/jobs/v1"
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

	// Jobs RPC client
	jobsRPC JobsRPCer

	// SMTP server components
	smtpServer *smtp.Server
	listener   net.Listener
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
	p.startCleanupRoutine(context.Background())

	return errCh
}

// Stop gracefully stops the plugin
func (p *Plugin) Stop(ctx context.Context) error {
	p.log.Info("stopping SMTP plugin")

	doneCh := make(chan struct{}, 1)

	go func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		// 1. Close listener (stops accepting new connections)
		if p.listener != nil {
			_ = p.listener.Close()
		}

		// 2. Close SMTP server
		if p.smtpServer != nil {
			_ = p.smtpServer.Close()
		}

		// 3. Close all tracked connections
		p.connections.Range(func(key, value any) bool {
			// Sessions will be cleaned up by Logout()
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
			jobsRPC := pp.(JobsRPCer)
			p.jobsRPC = jobsRPC
		}, (*JobsRPCer)(nil)),
	}
}

// RPC returns RPC interface for external management
func (p *Plugin) RPC() any {
	return &rpc{p: p}
}

// pushToJobs sends email as job to Jobs plugin
func (p *Plugin) pushToJobs(email *EmailData) error {
	const op = errors.Op("smtp_push_to_jobs")

	if p.jobsRPC == nil {
		return errors.E(op, errors.Str("jobs RPC not available"))
	}

	req := ToJobsRequest(email, &p.cfg.Jobs)

	var empty jobsProto.Empty
	err := p.jobsRPC.Push(req, &empty)
	if err != nil {
		return errors.E(op, err)
	}

	p.log.Debug("email pushed to jobs",
		zap.String("uuid", email.UUID),
		zap.String("pipeline", p.cfg.Jobs.Pipeline),
	)

	return nil
}

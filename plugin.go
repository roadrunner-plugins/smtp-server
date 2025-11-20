package smtp

import (
	"context"
	"net"
	"sync"

	"github.com/emersion/go-smtp"
	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/pool/payload"
	"github.com/roadrunner-server/pool/pool"
	staticPool "github.com/roadrunner-server/pool/pool/static_pool"
	"github.com/roadrunner-server/pool/state/process"
	"github.com/roadrunner-server/pool/worker"
	"go.uber.org/zap"
)

const (
	PluginName = "smtp"
	RrMode     = "RR_MODE"
)

// Pool interface for worker pool operations
type Pool interface {
	// Workers returns worker list associated with the pool
	Workers() (workers []*worker.Process)
	// RemoveWorker removes worker from the pool
	RemoveWorker(ctx context.Context) error
	// AddWorker adds worker to the pool
	AddWorker() error
	// Exec executes payload
	Exec(ctx context.Context, p *payload.Payload, stopCh chan struct{}) (chan *staticPool.PExec, error)
	// Reset kills all workers and replaces with new
	Reset(ctx context.Context) error
	// Destroy all underlying stacks
	Destroy(ctx context.Context)
}

// Logger interface for dependency injection
type Logger interface {
	NamedLogger(name string) *zap.Logger
}

// Server creates workers for the application
type Server interface {
	NewPool(ctx context.Context, cfg *pool.Config, env map[string]string, _ *zap.Logger) (*staticPool.Pool, error)
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
	mu     sync.RWMutex
	cfg    *Config
	log    *zap.Logger
	server Server

	wPool       Pool
	connections sync.Map // uuid -> *Session
	pldPool     sync.Pool

	// SMTP server components
	smtpServer *smtp.Server
	listener   net.Listener
}

// Init initializes the plugin with configuration and logger
func (p *Plugin) Init(log Logger, cfg Configurer, server Server) error {
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

	// Initialize payload pool
	p.pldPool = sync.Pool{
		New: func() any {
			return new(payload.Payload)
		},
	}

	// Setup logger
	p.log = log.NamedLogger(PluginName)
	p.server = server

	p.log.Info("SMTP plugin initialized",
		zap.String("addr", p.cfg.Addr),
		zap.String("hostname", p.cfg.Hostname),
		zap.Int64("max_message_size", p.cfg.MaxMessageSize),
	)

	return nil
}

// Serve starts the SMTP server
func (p *Plugin) Serve() chan error {
	errCh := make(chan error, 1)

	var err error
	p.wPool, err = p.server.NewPool(context.Background(), p.cfg.Pool, map[string]string{RrMode: pluginName}, nil)
	if err != nil {
		errCh <- err
		return errCh
	}

	p.log.Info("SMTP plugin worker pool created",
		zap.Int("num_workers", len(p.wPool.Workers())),
	)

	// 2. Create SMTP backend
	backend := NewBackend(p)

	// 3. Create SMTP server
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
	)

	// 4. Create listener
	p.listener, err = net.Listen("tcp", p.cfg.Addr)
	if err != nil {
		errCh <- errors.E(errors.Op("smtp_listen"), err)
		return errCh
	}

	p.log.Info("SMTP listener created", zap.String("addr", p.cfg.Addr))

	// 5. Start SMTP server in goroutine
	go func() {
		p.log.Info("SMTP server starting", zap.String("addr", p.cfg.Addr))
		if err := p.smtpServer.Serve(p.listener); err != nil {
			p.log.Error("SMTP server error", zap.Error(err))
			errCh <- err
		}
	}()

	// 6. Start temp file cleanup routine
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

		if p.wPool != nil {
			switch pp := p.wPool.(type) {
			case *staticPool.Pool:
				if pp != nil {
					pp.Destroy(ctx)
				}
			default:
				// pool is nil, nothing to do
			}
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

// Reset resets the worker pool
func (p *Plugin) Reset() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	const op = errors.Op("smtp_reset")

	if p.wPool == nil {
		return nil
	}

	p.log.Info("resetting SMTP plugin workers")

	err := p.wPool.Reset(context.Background())
	if err != nil {
		return errors.E(op, err)
	}

	p.log.Info("SMTP plugin workers reset completed")
	return nil
}

func (p *Plugin) Workers() []*process.State {
	p.mu.RLock()
	wrk := p.wPool.Workers()
	p.mu.RUnlock()

	ps := make([]*process.State, len(wrk))

	for i := range wrk {
		st, err := process.WorkerProcessState(wrk[i])
		if err != nil {
			p.log.Error("jobs workers state", zap.Error(err))
			return nil
		}

		ps[i] = st
	}

	return ps
}

// Name returns plugin name for RoadRunner
func (p *Plugin) Name() string {
	return PluginName
}

// RPC returns the RPC interface
func (p *Plugin) RPC() any {
	return &rpc{
		p: p,
	}
}

// AddWorker adds a new worker to the pool
func (p *Plugin) AddWorker() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.wPool == nil {
		return errors.Str("worker pool not initialized")
	}

	return p.wPool.AddWorker()
}

// RemoveWorker removes a worker from the pool
func (p *Plugin) RemoveWorker(ctx context.Context) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.wPool == nil {
		return errors.Str("worker pool not initialized")
	}

	return p.wPool.RemoveWorker(ctx)
}

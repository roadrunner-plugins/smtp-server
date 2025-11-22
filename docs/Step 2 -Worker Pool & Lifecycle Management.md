## Goal

Implement worker pool creation, lifecycle management (Serve/Stop), and RPC interface for pool management. After this step, PHP workers will start and the plugin will have graceful shutdown capabilities.

---

## Components to Implement

### 2.1 Extended Plugin Structure

```go
package smtp

import (
    "context"
    "sync"
)

type Plugin struct {
    mu     sync.RWMutex  // Protects pool access during Reset/Stop
    cfg    *Config
    log    *zap.Logger
    server Server
    
    // Worker pool (created in Serve)
    pool Pool
}
```

### 2.2 Pool Interface

```go
// Pool interface from RoadRunner SDK
type Pool interface {
    // Workers returns list of workers in the pool
    Workers() []*worker.Process
    
    // RemoveWorker removes worker from the pool
    RemoveWorker(ctx context.Context) error
    
    // AddWorker adds worker to the pool
    AddWorker() error
    
    // Exec sends payload to worker and returns response
    Exec(ctx context.Context, p *payload.Payload, stopCh chan struct{}) (chan *staticPool.PExec, error)
    
    // Reset kills all workers and creates new ones
    Reset(ctx context.Context) error
    
    // Destroy terminates all workers (graceful shutdown)
    Destroy(ctx context.Context)
}
```

### 2.3 Serve Method

```go
// Serve starts the plugin and creates worker pool
func (p *Plugin) Serve() chan error {
    errCh := make(chan error, 1)
    
    p.mu.Lock()
    defer p.mu.Unlock()
    
    // Create worker pool
    pool, err := p.server.NewPool(
        context.Background(),
        p.cfg.Pool,
        map[string]string{
            "RR_MODE": "smtp",  // Environment variable for PHP workers
        },
        p.log,
    )
    if err != nil {
        errCh <- errors.E("smtp_serve", err)
        return errCh
    }
    
    p.pool = pool
    
    p.log.Info("SMTP plugin worker pool created",
        zap.Int("num_workers", len(p.pool.Workers())),
    )
    
    // SMTP server will be started in Step 3
    // For now, just return empty error channel (plugin is "serving")
    
    return errCh
}
```

### 2.4 Stop Method

```go
// Stop gracefully stops the plugin
func (p *Plugin) Stop(ctx context.Context) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    p.log.Info("stopping SMTP plugin")
    
    doneCh := make(chan struct{}, 1)
    
    go func() {
        // SMTP server shutdown will be added in Step 3
        
        // Destroy worker pool
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

### 2.5 Reset Method

```go
// Reset replaces worker pool with new workers
func (p *Plugin) Reset() error {
    const op = errors.Op("smtp_reset")
    
    p.mu.Lock()
    defer p.mu.Unlock()
    
    if p.pool == nil {
        return nil
    }
    
    p.log.Info("resetting SMTP plugin workers")
    
    err := p.pool.Reset(context.Background())
    if err != nil {
        return errors.E(op, err)
    }
    
    p.log.Info("SMTP plugin workers reset completed")
    
    return nil
}
```

### 2.6 Worker Statistics

```go
// Workers returns worker process states (for monitoring/RPC)
func (p *Plugin) Workers() []*process.State {
    p.mu.RLock()
    defer p.mu.RUnlock()
    
    if p.pool == nil {
        return nil
    }
    
    workers := p.pool.Workers()
    states := make([]*process.State, len(workers))
    
    for i, w := range workers {
        state, err := process.WorkerProcessState(w)
        if err != nil {
            p.log.Error("failed to get worker state", zap.Error(err))
            continue
        }
        states[i] = state
    }
    
    return states
}
```

### 2.7 RPC Interface

```go
// RPC returns RPC interface for external management
func (p *Plugin) RPC() any {
    return &rpc{p: p}
}

type rpc struct {
    p *Plugin
}

// AddWorker adds new worker to the pool
func (r *rpc) AddWorker(_ bool, success *bool) error {
    *success = false
    
    err := r.p.AddWorker()
    if err != nil {
        return err
    }
    
    *success = true
    return nil
}

// RemoveWorker removes worker from the pool
func (r *rpc) RemoveWorker(_ bool, success *bool) error {
    *success = false
    
    err := r.p.RemoveWorker(context.Background())
    if err != nil {
        return err
    }
    
    *success = true
    return nil
}

// WorkersList returns list of active workers
func (r *rpc) WorkersList(_ bool, workers *[]*process.State) error {
    *workers = r.p.Workers()
    return nil
}
```

### 2.8 Worker Management Methods

```go
// AddWorker adds a new worker to the pool
func (p *Plugin) AddWorker() error {
    p.mu.RLock()
    defer p.mu.RUnlock()
    
    if p.pool == nil {
        return errors.Str("worker pool not initialized")
    }
    
    return p.pool.AddWorker()
}

// RemoveWorker removes a worker from the pool
func (p *Plugin) RemoveWorker(ctx context.Context) error {
    p.mu.RLock()
    defer p.mu.RUnlock()
    
    if p.pool == nil {
        return errors.Str("worker pool not initialized")
    }
    
    return p.pool.RemoveWorker(ctx)
}
```

---

## File Structure Update

```
smtp/
├── go.mod
├── config.go
├── plugin.go           # Updated: Serve, Stop, Reset, Workers
├── rpc.go             # NEW: RPC interface
├── workers_manager.go  # NEW: AddWorker, RemoveWorker
└── .plugin.yaml
```

---

## Dependencies Update (go.mod)

```go
module github.com/buggregator/smtp-server

go 1.23

require (
    github.com/roadrunner-server/errors v1.4.1
    github.com/roadrunner-server/pool v1.1.3
    github.com/roadrunner-server/api/v4 v4.x.x  // For worker.Process, process.State
    go.uber.org/zap v1.27.0
)
```

---

## PHP Worker Example (for testing)

```php
<?php
// worker.php

use Spiral\RoadRunner\Worker;
use Spiral\RoadRunner\Http\PSR7Worker;

require __DIR__ . '/vendor/autoload.php';

$worker = Worker::create();

// For now, worker just waits (no SMTP handling yet)
// This proves worker pool is working

while ($payload = $worker->waitPayload()) {
    error_log("[SMTP Worker] Received payload");
    
    // Echo back (Step 4 will add email processing)
    $worker->respond(new \Spiral\RoadRunner\Payload('CONTINUE'));
}
```

---

## Verification

After this step:

1. ✅ Plugin starts successfully (`rr serve`)
2. ✅ Worker pool is created with configured number of workers
3. ✅ Workers appear in `rr workers list` output
4. ✅ RPC commands work:
    - `rr rpc smtp.WorkersList` - shows workers
    - `rr rpc smtp.AddWorker` - adds worker
    - `rr rpc smtp.RemoveWorker` - removes worker
5. ✅ `rr reset smtp` resets worker pool
6. ✅ Graceful shutdown works (Ctrl+C or `rr stop`)
7. ✅ Workers log shows they are started and waiting

**What's NOT working yet**:

- ❌ No SMTP server listening (Step 3)
- ❌ Workers don't receive SMTP events (Step 4)

---

## Testing Commands

```bash
# Start RoadRunner
rr serve

# Check workers
rr workers list

# RPC commands
rr rpc smtp.WorkersList
rr rpc smtp.AddWorker
rr rpc smtp.RemoveWorker

# Reset workers
rr reset smtp

# Stop gracefully
rr stop
```

---

## Next Step Preview

**Step 3** will implement the SMTP server listener using `emersion/go-smtp`, handling connections and basic SMTP protocol commands (HELO, MAIL FROM, RCPT TO, DATA, QUIT).
package smtp

import (
	"context"
)

// AddWorker adds a new PHP worker to the pool
func (p *Plugin) AddWorker() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.wPool.AddWorker()
}

// RemoveWorker removes a PHP worker from the pool
func (p *Plugin) RemoveWorker(ctx context.Context) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.wPool.RemoveWorker(ctx)
}

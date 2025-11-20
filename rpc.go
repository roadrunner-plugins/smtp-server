package smtp

import (
	"context"

	"github.com/roadrunner-server/pool/state/process"
)

// rpc provides RPC interface for external management
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

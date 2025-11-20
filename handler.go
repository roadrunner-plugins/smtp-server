package smtp

import (
	"context"
	"time"

	"github.com/goccy/go-json"
	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/pool/payload"
	"go.uber.org/zap"
)

// sendToWorker sends email data to PHP worker and waits for response
func (s *Session) sendToWorker(emailData *EmailData) (string, error) {
	// 1. Marshal email data to JSON
	jsonData, err := json.Marshal(emailData)
	if err != nil {
		return "", errors.E(errors.Op("smtp_marshal_email"), err)
	}

	// 2. Create payload
	pld := &payload.Payload{
		Context: jsonData, // Email data in context
		Body:    nil,      // No body needed
	}

	// 3. Execute via worker pool
	ctx := context.Background()
	stopCh := make(chan struct{}, 1)

	s.backend.plugin.mu.RLock()
	pool := s.backend.plugin.wPool
	s.backend.plugin.mu.RUnlock()

	if pool == nil {
		return "", errors.Str("worker pool not initialized")
	}

	result, err := pool.Exec(ctx, pld, stopCh)
	if err != nil {
		return "", errors.E(errors.Op("smtp_worker_exec"), err)
	}

	// 4. Read response from worker
	select {
	case resp := <-result:
		if resp.Error() != nil {
			return "", resp.Error()
		}

		// Get response from context
		response := string(resp.Payload().Context)

		s.log.Debug("worker response",
			zap.String("uuid", s.uuid),
			zap.String("response", response),
		)

		return response, nil

	case <-time.After(30 * time.Second):
		return "", errors.Str("worker timeout")
	}
}

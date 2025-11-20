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
	s.log.Debug("sendToWorker called",
		zap.String("uuid", s.uuid),
		zap.String("from", emailData.Envelope.From),
		zap.Strings("to", emailData.Envelope.To),
	)

	// 1. Marshal email data to JSON
	jsonData, err := json.Marshal(emailData)
	if err != nil {
		s.log.Error("failed to marshal email data", zap.Error(err))
		return "", errors.E(errors.Op("smtp_marshal_email"), err)
	}

	s.log.Debug("payload marshaled",
		zap.String("uuid", s.uuid),
		zap.Int("json_size", len(jsonData)),
	)

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
		s.log.Error("worker pool not initialized")
		return "", errors.Str("worker pool not initialized")
	}

	s.log.Debug("executing payload on worker pool", zap.String("uuid", s.uuid))
	result, err := pool.Exec(ctx, pld, stopCh)
	if err != nil {
		s.log.Error("worker pool exec failed", zap.String("uuid", s.uuid), zap.Error(err))
		return "", errors.E(errors.Op("smtp_worker_exec"), err)
	}
	s.log.Debug("payload sent to worker, waiting for response", zap.String("uuid", s.uuid))

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

package handler

import (
	"bytes"
	"sync"
	"time"

	"github.com/roadrunner-server/pool/payload"
)

// PoolHelper manages sync.Pool objects for resource reuse
type PoolHelper struct {
	emailEventPool *sync.Pool
	payloadPool    *sync.Pool
	bufferPool     *sync.Pool
}

// NewPoolHelper creates pool helper with initialized pools
func NewPoolHelper(emailEventPool, payloadPool, bufferPool *sync.Pool) *PoolHelper {
	return &PoolHelper{
		emailEventPool: emailEventPool,
		payloadPool:    payloadPool,
		bufferPool:     bufferPool,
	}
}

// GetEmailEvent retrieves an EmailEvent from pool
func (ph *PoolHelper) GetEmailEvent() *EmailEvent {
	return ph.emailEventPool.Get().(*EmailEvent)
}

// PutEmailEvent returns an EmailEvent to pool after resetting fields
func (ph *PoolHelper) PutEmailEvent(ev *EmailEvent) {
	// Reset all fields to avoid memory leaks
	ev.Event = ""
	ev.Server = ""
	ev.UUID = ""
	ev.RemoteAddr = ""
	ev.ReceivedAt = time.Time{}
	
	ev.Envelope.From = ""
	ev.Envelope.To = nil
	ev.Envelope.Helo = ""
	
	ev.Authentication = nil
	
	ev.Message.Headers = nil
	ev.Message.Body = ""
	ev.Message.Raw = ""
	
	ev.Attachments = nil
	
	ph.emailEventPool.Put(ev)
}

// GetPayload retrieves a payload.Payload from pool
func (ph *PoolHelper) GetPayload() *payload.Payload {
	return ph.payloadPool.Get().(*payload.Payload)
}

// PutPayload returns a payload.Payload to pool after resetting
func (ph *PoolHelper) PutPayload(pld *payload.Payload) {
	pld.Body = nil
	pld.Context = nil
	ph.payloadPool.Put(pld)
}

// GetBuffer retrieves a bytes.Buffer from pool
func (ph *PoolHelper) GetBuffer() *bytes.Buffer {
	return ph.bufferPool.Get().(*bytes.Buffer)
}

// PutBuffer returns a bytes.Buffer to pool after resetting
func (ph *PoolHelper) PutBuffer(buf *bytes.Buffer) {
	buf.Reset()
	ph.bufferPool.Put(buf)
}

package smtp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"go.uber.org/zap"
)

// mockJobs implements the Jobs interface for testing
type mockJobs struct {
	pushed []jobs.Message
	err    error
}

func (m *mockJobs) Push(_ context.Context, msg jobs.Message) error {
	if m.err != nil {
		return m.err
	}
	m.pushed = append(m.pushed, msg)
	return nil
}

func TestEmailToJobMessage_Success(t *testing.T) {
	email := &EmailData{
		Event:      "EMAIL_RECEIVED",
		UUID:       "test-uuid-123",
		RemoteAddr: "127.0.0.1:12345",
		ReceivedAt: time.Now(),
		Envelope: EnvelopeData{
			From: []EmailAddress{{Email: "sender@test.com", Name: "Sender"}},
			To:   []EmailAddress{{Email: "recipient@test.com"}},
		},
		Message: MessageData{
			Subject: "Test Subject",
			Body:    "Test body",
		},
	}

	cfg := &JobsConfig{
		Pipeline: "smtp-emails",
		Priority: 5,
		Delay:    10,
		AutoAck:  true,
	}

	msg, err := emailToJobMessage(email, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.Name() != "smtp.email" {
		t.Errorf("expected job name smtp.email, got %s", msg.Name())
	}
	if msg.Priority() != 5 {
		t.Errorf("expected priority 5, got %d", msg.Priority())
	}
	if msg.Delay() != 10 {
		t.Errorf("expected delay 10, got %d", msg.Delay())
	}
	if msg.AutoAck() != true {
		t.Error("expected auto_ack true")
	}
	if msg.GroupID() != "smtp-emails" {
		t.Errorf("expected group smtp-emails, got %s", msg.GroupID())
	}

	// Check headers
	headers := msg.Headers()
	if v := headers["uuid"]; len(v) == 0 || v[0] != "test-uuid-123" {
		t.Errorf("expected uuid header, got %v", v)
	}
	if v := headers["payload_class"]; len(v) == 0 || v[0] != "smtp:handler" {
		t.Errorf("expected payload_class header, got %v", v)
	}

	// Verify payload is valid JSON
	var decoded EmailData
	if err := json.Unmarshal(msg.Payload(), &decoded); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if decoded.UUID != "test-uuid-123" {
		t.Errorf("expected UUID test-uuid-123 in payload, got %s", decoded.UUID)
	}
	if decoded.Message.Subject != "Test Subject" {
		t.Errorf("expected subject in payload, got %s", decoded.Message.Subject)
	}
}

func TestEmailToJobMessage_UniqueIDs(t *testing.T) {
	email := &EmailData{UUID: "test"}
	cfg := &JobsConfig{Pipeline: "test"}

	msg1, err := emailToJobMessage(email, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg2, err := emailToJobMessage(email, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg1.ID() == msg2.ID() {
		t.Error("expected unique job IDs for each call")
	}
}

func TestPushToJobs_Success(t *testing.T) {
	log, _ := zap.NewDevelopment()
	mock := &mockJobs{}
	p := &Plugin{
		jobs: mock,
		cfg:  &Config{Jobs: JobsConfig{Pipeline: "test", Priority: 10}},
		log:  log,
	}

	email := &EmailData{
		UUID: "push-test",
		Message: MessageData{
			Subject: "Push Test",
			Body:    "body",
		},
	}

	err := p.pushToJobs(email)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.pushed) != 1 {
		t.Fatalf("expected 1 push, got %d", len(mock.pushed))
	}
	if mock.pushed[0].Name() != "smtp.email" {
		t.Errorf("expected job name smtp.email, got %s", mock.pushed[0].Name())
	}
}

func TestPushToJobs_Error(t *testing.T) {
	log, _ := zap.NewDevelopment()
	mock := &mockJobs{err: errors.New("push failed")}
	p := &Plugin{
		jobs: mock,
		cfg:  &Config{Jobs: JobsConfig{Pipeline: "test", Priority: 10}},
		log:  log,
	}

	email := &EmailData{UUID: "error-test"}
	err := p.pushToJobs(email)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestPushToJobs_NilJobsPlugin(t *testing.T) {
	log, _ := zap.NewDevelopment()
	p := &Plugin{
		jobs: nil,
		cfg:  &Config{Jobs: JobsConfig{Pipeline: "test"}},
		log:  log,
	}

	email := &EmailData{UUID: "nil-jobs-test"}
	err := p.pushToJobs(email)
	if err == nil {
		t.Error("expected error for nil jobs plugin")
	}
}

func TestJobInterfaceMethods(t *testing.T) {
	job := &Job{
		Job:   "test.job",
		Ident: "job-123",
		Pld:   []byte(`{"key":"value"}`),
		Hdr:   map[string][]string{"h1": {"v1"}},
		Options: &JobOptions{
			Priority: 5,
			Pipeline: "pipe",
			Delay:    100,
			AutoAck:  true,
		},
	}

	if job.ID() != "job-123" {
		t.Errorf("ID() = %s, want job-123", job.ID())
	}
	if job.Name() != "test.job" {
		t.Errorf("Name() = %s, want test.job", job.Name())
	}
	if job.GroupID() != "pipe" {
		t.Errorf("GroupID() = %s, want pipe", job.GroupID())
	}
	if job.Priority() != 5 {
		t.Errorf("Priority() = %d, want 5", job.Priority())
	}
	if job.Delay() != 100 {
		t.Errorf("Delay() = %d, want 100", job.Delay())
	}
	if job.AutoAck() != true {
		t.Error("AutoAck() = false, want true")
	}
	if string(job.Payload()) != `{"key":"value"}` {
		t.Errorf("Payload() = %s", string(job.Payload()))
	}
	if job.Headers()["h1"][0] != "v1" {
		t.Errorf("Headers() missing h1")
	}

	// Kafka methods should return zero values
	if job.Offset() != 0 {
		t.Errorf("Offset() = %d, want 0", job.Offset())
	}
	if job.Partition() != 0 {
		t.Errorf("Partition() = %d, want 0", job.Partition())
	}
	if job.Topic() != "" {
		t.Errorf("Topic() = %s, want empty", job.Topic())
	}
	if job.Metadata() != "" {
		t.Errorf("Metadata() = %s, want empty", job.Metadata())
	}
}

func TestJobInterfaceMethods_NilOptions(t *testing.T) {
	job := &Job{
		Job:   "test.job",
		Ident: "job-nil",
	}

	if job.GroupID() != "" {
		t.Errorf("GroupID() should be empty with nil options, got %s", job.GroupID())
	}
	if job.Priority() != 10 {
		t.Errorf("Priority() should default to 10, got %d", job.Priority())
	}
	if job.Delay() != 0 {
		t.Errorf("Delay() should be 0 with nil options, got %d", job.Delay())
	}
	if job.AutoAck() != false {
		t.Error("AutoAck() should be false with nil options")
	}
}

func TestJobUpdatePriority(t *testing.T) {
	job := &Job{Ident: "test"}
	job.UpdatePriority(42)

	if job.Options == nil {
		t.Fatal("Options should be created")
	}
	if job.Options.Priority != 42 {
		t.Errorf("expected priority 42, got %d", job.Options.Priority)
	}

	// Update again
	job.UpdatePriority(1)
	if job.Options.Priority != 1 {
		t.Errorf("expected priority 1, got %d", job.Options.Priority)
	}
}

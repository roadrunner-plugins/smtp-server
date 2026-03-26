package smtp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"go.uber.org/zap"
)

// capturingJobs captures pushed messages for inspection
type capturingJobs struct {
	messages []jobs.Message
}

func (c *capturingJobs) Push(_ context.Context, msg jobs.Message) error {
	c.messages = append(c.messages, msg)
	return nil
}

func newTestPlugin(mode string) (*Plugin, *capturingJobs) {
	log, _ := zap.NewDevelopment()
	capture := &capturingJobs{}
	cfg := &Config{
		AttachmentStorage: AttachmentConfig{Mode: mode},
		Jobs:              JobsConfig{Pipeline: "test", Priority: 10},
	}
	p := &Plugin{
		cfg:  cfg,
		log:  log,
		jobs: capture,
	}
	return p, capture
}

func newTestSessionWithPlugin(p *Plugin) *Session {
	b := &Backend{plugin: p, log: p.log}
	return &Session{
		backend:    b,
		uuid:       "sess-00000000",
		remoteAddr: "127.0.0.1:9999",
		from:       "sender@test.com",
		to:         []string{"recipient@test.com"},
		log:        p.log,
	}
}

func TestSession_DataFlow(t *testing.T) {
	p, capture := newTestPlugin("memory")
	s := newTestSessionWithPlugin(p)

	raw := "From: sender@test.com\r\nTo: recipient@test.com\r\nSubject: Integration\r\n\r\nHello from test"
	err := s.Data(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Data() error: %v", err)
	}

	if len(capture.messages) != 1 {
		t.Fatalf("expected 1 pushed message, got %d", len(capture.messages))
	}

	msg := capture.messages[0]
	var email EmailData
	if err := json.Unmarshal(msg.Payload(), &email); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if email.Event != "EMAIL_RECEIVED" {
		t.Errorf("expected event EMAIL_RECEIVED, got %s", email.Event)
	}
	if email.UUID != "sess-00000000" {
		t.Errorf("expected UUID sess-00000000, got %s", email.UUID)
	}
	if email.RemoteAddr != "127.0.0.1:9999" {
		t.Errorf("expected remote addr 127.0.0.1:9999, got %s", email.RemoteAddr)
	}
	if email.Message.Subject != "Integration" {
		t.Errorf("expected subject Integration, got %s", email.Message.Subject)
	}
	if email.Message.Body != "Hello from test" {
		t.Errorf("expected body 'Hello from test', got %q", email.Message.Body)
	}
	if len(email.Envelope.From) != 1 || email.Envelope.From[0].Email != "sender@test.com" {
		t.Errorf("unexpected envelope from: %+v", email.Envelope.From)
	}
}

func TestSession_DataWithAttachment_MemoryMode(t *testing.T) {
	p, capture := newTestPlugin("memory")
	s := newTestSessionWithPlugin(p)

	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: Att\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"Body\r\n" +
		"--b\r\n" +
		"Content-Disposition: attachment; filename=\"test.txt\"\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"attachment data\r\n" +
		"--b--\r\n"

	err := s.Data(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Data() error: %v", err)
	}

	var email EmailData
	json.Unmarshal(capture.messages[0].Payload(), &email)

	if len(email.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(email.Attachments))
	}

	att := email.Attachments[0]
	if att.Filename != "test.txt" {
		t.Errorf("expected filename test.txt, got %s", att.Filename)
	}
	if att.Content == "" {
		t.Error("expected Content to be set in memory mode")
	}
	if att.Path != "" {
		t.Error("expected Path to be empty in memory mode")
	}
}

func TestSession_DataWithAttachment_TempfileMode(t *testing.T) {
	tmpDir := t.TempDir()
	log, _ := zap.NewDevelopment()
	capture := &capturingJobs{}
	cfg := &Config{
		AttachmentStorage: AttachmentConfig{
			Mode:    "tempfile",
			TempDir: tmpDir,
		},
		Jobs: JobsConfig{Pipeline: "test", Priority: 10},
	}
	p := &Plugin{cfg: cfg, log: log, jobs: capture}
	s := newTestSessionWithPlugin(p)

	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: TempAtt\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"Body\r\n" +
		"--b\r\n" +
		"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
		"Content-Type: application/pdf\r\n\r\n" +
		"fake pdf\r\n" +
		"--b--\r\n"

	err := s.Data(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Data() error: %v", err)
	}

	var email EmailData
	json.Unmarshal(capture.messages[0].Payload(), &email)

	att := email.Attachments[0]
	if att.Path == "" {
		t.Error("expected Path to be set in tempfile mode")
	}
	if att.Content != "" {
		t.Error("expected Content to be empty in tempfile mode")
	}
	if !strings.HasPrefix(att.Path, tmpDir) {
		t.Errorf("expected path under %s, got %s", tmpDir, att.Path)
	}
}

func TestSession_DataWithAuth(t *testing.T) {
	p, capture := newTestPlugin("memory")
	s := newTestSessionWithPlugin(p)
	s.authenticated = true
	s.authUsername = "user@test.com"
	s.authPassword = "secret"
	s.authMechanism = "PLAIN"

	raw := "From: sender@test.com\r\nTo: recipient@test.com\r\nSubject: Auth\r\n\r\nBody"
	err := s.Data(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Data() error: %v", err)
	}

	var email EmailData
	json.Unmarshal(capture.messages[0].Payload(), &email)

	if email.Auth == nil {
		t.Fatal("expected auth data")
	}
	if !email.Auth.Attempted {
		t.Error("expected auth attempted = true")
	}
	if email.Auth.Username != "user@test.com" {
		t.Errorf("expected username user@test.com, got %s", email.Auth.Username)
	}
	if email.Auth.Mechanism != "PLAIN" {
		t.Errorf("expected mechanism PLAIN, got %s", email.Auth.Mechanism)
	}
}

func TestSession_DataWithoutAuth(t *testing.T) {
	p, capture := newTestPlugin("memory")
	s := newTestSessionWithPlugin(p)

	raw := "From: sender@test.com\r\nTo: recipient@test.com\r\nSubject: NoAuth\r\n\r\nBody"
	s.Data(strings.NewReader(raw))

	var email EmailData
	json.Unmarshal(capture.messages[0].Payload(), &email)

	if email.Auth != nil {
		t.Error("expected nil auth when not authenticated")
	}
}

func TestSession_HeadersPassedThrough(t *testing.T) {
	p, capture := newTestPlugin("memory")
	s := newTestSessionWithPlugin(p)

	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: Headers\r\n" +
		"X-Priority: 1\r\n" +
		"Date: Thu, 01 Jan 2025 00:00:00 +0000\r\n\r\nBody"

	s.Data(strings.NewReader(raw))

	var email EmailData
	json.Unmarshal(capture.messages[0].Payload(), &email)

	if email.Message.Headers == nil {
		t.Fatal("headers should not be nil")
	}
	if v, ok := email.Message.Headers["X-Priority"]; !ok || v[0] != "1" {
		t.Errorf("expected X-Priority header, got %v", email.Message.Headers["X-Priority"])
	}
	if _, ok := email.Message.Headers["Date"]; !ok {
		t.Error("expected Date header to be preserved")
	}
}

func TestSession_MailAndRcpt(t *testing.T) {
	p, _ := newTestPlugin("memory")
	s := newTestSessionWithPlugin(p)

	s.Mail("from@test.com", nil)
	if s.from != "from@test.com" {
		t.Errorf("expected from from@test.com, got %s", s.from)
	}

	s.Rcpt("to1@test.com", nil)
	s.Rcpt("to2@test.com", nil)
	if len(s.to) != 3 { // 1 from newTestSessionWithPlugin + 2 added
		t.Errorf("expected 3 recipients, got %d", len(s.to))
	}
}

func TestSession_Reset(t *testing.T) {
	p, _ := newTestPlugin("memory")
	s := newTestSessionWithPlugin(p)
	s.emailData = *bytes.NewBufferString("some data")

	s.Reset()

	if s.from != "" {
		t.Error("from should be empty after reset")
	}
	if s.to != nil {
		t.Error("to should be nil after reset")
	}
	if s.emailData.Len() != 0 {
		t.Error("emailData should be empty after reset")
	}
}

func TestSession_Logout(t *testing.T) {
	p, _ := newTestPlugin("memory")
	s := newTestSessionWithPlugin(p)

	// Store session in connections map
	p.connections.Store(s.uuid, s)

	err := s.Logout()
	if err != nil {
		t.Fatalf("Logout() error: %v", err)
	}

	// Should be removed from connections
	if _, ok := p.connections.Load(s.uuid); ok {
		t.Error("session should be removed from connections after Logout")
	}
}

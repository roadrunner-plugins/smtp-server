package smtp

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func newTestSession(cfg *Config) *Session {
	if cfg == nil {
		cfg = &Config{
			AttachmentStorage: AttachmentConfig{Mode: "memory"},
		}
	}
	log, _ := zap.NewDevelopment()
	p := &Plugin{cfg: cfg, log: log}
	b := &Backend{plugin: p, log: log}
	return &Session{
		backend: b,
		uuid:    "00000000-0000-0000-0000-000000000000",
		to:      []string{"recipient@test.com"},
		log:     log,
	}
}

func TestParseEmail_SimplePlainText(t *testing.T) {
	raw := "From: sender@test.com\r\nTo: recipient@test.com\r\nSubject: Hello\r\n\r\nThis is the body."
	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.Subject != "Hello" {
		t.Errorf("expected subject Hello, got %s", parsed.Subject)
	}
	if parsed.TextBody != "This is the body." {
		t.Errorf("expected body 'This is the body.', got %q", parsed.TextBody)
	}
	if len(parsed.Sender) != 1 || parsed.Sender[0].Email != "sender@test.com" {
		t.Errorf("unexpected sender: %+v", parsed.Sender)
	}
	if len(parsed.Recipients) != 1 || parsed.Recipients[0].Email != "recipient@test.com" {
		t.Errorf("unexpected recipients: %+v", parsed.Recipients)
	}
}

func TestParseEmail_HTMLBody(t *testing.T) {
	raw := "From: sender@test.com\r\nTo: recipient@test.com\r\nSubject: HTML\r\nContent-Type: text/html\r\n\r\n<h1>Hello</h1>"
	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.HTMLBody != "<h1>Hello</h1>" {
		t.Errorf("expected HTML body, got %q", parsed.HTMLBody)
	}
	if parsed.TextBody != "" {
		t.Errorf("expected empty text body, got %q", parsed.TextBody)
	}
}

func TestParseEmail_Base64BodyWithLineBreaks(t *testing.T) {
	// Encode body as base64 with line breaks (RFC 2045 style, 76 chars per line)
	body := "This is a test body that is long enough to require line breaks in base64 encoding format."
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	// Insert line breaks every 76 chars like real email
	var lines []string
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		lines = append(lines, encoded[i:end])
	}
	encodedWithBreaks := strings.Join(lines, "\r\n")

	raw := fmt.Sprintf("From: sender@test.com\r\nTo: recipient@test.com\r\nSubject: Base64\r\nContent-Transfer-Encoding: base64\r\n\r\n%s", encodedWithBreaks)
	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.TextBody != body {
		t.Errorf("expected decoded body %q, got %q", body, parsed.TextBody)
	}
}

func TestParseEmail_QuotedPrintable(t *testing.T) {
	raw := "From: sender@test.com\r\nTo: recipient@test.com\r\nSubject: QP\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nHello =3D World"
	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.TextBody != "Hello = World" {
		t.Errorf("expected decoded QP body 'Hello = World', got %q", parsed.TextBody)
	}
}

func TestParseEmail_MultipartMixed(t *testing.T) {
	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: Multipart\r\n" +
		"Content-Type: multipart/mixed; boundary=\"boundary1\"\r\n\r\n" +
		"--boundary1\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"Plain text body\r\n" +
		"--boundary1\r\n" +
		"Content-Type: text/html\r\n\r\n" +
		"<p>HTML body</p>\r\n" +
		"--boundary1\r\n" +
		"Content-Disposition: attachment; filename=\"test.txt\"\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"attachment content\r\n" +
		"--boundary1--\r\n"

	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.TextBody != "Plain text body" {
		t.Errorf("expected text body 'Plain text body', got %q", parsed.TextBody)
	}
	if parsed.HTMLBody != "<p>HTML body</p>" {
		t.Errorf("expected HTML body '<p>HTML body</p>', got %q", parsed.HTMLBody)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}
	if parsed.Attachments[0].Filename != "test.txt" {
		t.Errorf("expected filename test.txt, got %s", parsed.Attachments[0].Filename)
	}
	if parsed.Attachments[0].Size != int64(len("attachment content")) {
		t.Errorf("expected attachment size %d, got %d", len("attachment content"), parsed.Attachments[0].Size)
	}
}

func TestParseEmail_NestedMultipart(t *testing.T) {
	// multipart/mixed containing multipart/alternative + attachment
	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: Nested\r\n" +
		"Content-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
		"--outer\r\n" +
		"Content-Type: multipart/alternative; boundary=\"inner\"\r\n\r\n" +
		"--inner\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"Plain text\r\n" +
		"--inner\r\n" +
		"Content-Type: text/html\r\n\r\n" +
		"<p>HTML text</p>\r\n" +
		"--inner--\r\n" +
		"--outer\r\n" +
		"Content-Disposition: attachment; filename=\"file.pdf\"\r\n" +
		"Content-Type: application/pdf\r\n\r\n" +
		"fake pdf content\r\n" +
		"--outer--\r\n"

	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.TextBody != "Plain text" {
		t.Errorf("expected nested text body 'Plain text', got %q", parsed.TextBody)
	}
	if parsed.HTMLBody != "<p>HTML text</p>" {
		t.Errorf("expected nested HTML body '<p>HTML text</p>', got %q", parsed.HTMLBody)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}
	if parsed.Attachments[0].Filename != "file.pdf" {
		t.Errorf("expected filename file.pdf, got %s", parsed.Attachments[0].Filename)
	}
}

func TestParseEmail_Base64Attachment(t *testing.T) {
	content := "Hello, this is binary content!"
	// Encode with line breaks like real email
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	var lines []string
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		lines = append(lines, encoded[i:end])
	}
	encodedWithBreaks := strings.Join(lines, "\r\n")

	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: Attachment\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"Body\r\n" +
		"--b\r\n" +
		"Content-Disposition: attachment; filename=\"data.bin\"\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		encodedWithBreaks + "\r\n" +
		"--b--\r\n"

	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}

	att := parsed.Attachments[0]
	// In memory mode, content is re-encoded as base64 (without line breaks)
	decoded, err := base64.StdEncoding.DecodeString(att.Content)
	if err != nil {
		t.Fatalf("failed to decode attachment content: %v", err)
	}
	if string(decoded) != content {
		t.Errorf("expected attachment content %q, got %q", content, string(decoded))
	}
	if att.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), att.Size)
	}
}

func TestParseEmail_TempfileAttachmentMode(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		AttachmentStorage: AttachmentConfig{
			Mode:    "tempfile",
			TempDir: tmpDir,
		},
	}

	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: TempFile\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"Body\r\n" +
		"--b\r\n" +
		"Content-Disposition: attachment; filename=\"doc.txt\"\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"file content here\r\n" +
		"--b--\r\n"

	s := newTestSession(cfg)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}

	att := parsed.Attachments[0]
	// Content field should be a file path
	if !strings.HasPrefix(att.Content, tmpDir) {
		t.Errorf("expected temp file path under %s, got %s", tmpDir, att.Content)
	}

	// Verify file exists and has correct content
	data, err := os.ReadFile(att.Content)
	if err != nil {
		t.Fatalf("failed to read temp file: %v", err)
	}
	if string(data) != "file content here" {
		t.Errorf("expected file content 'file content here', got %q", string(data))
	}
}

func TestParseEmail_MessageID(t *testing.T) {
	raw := "From: sender@test.com\r\nTo: recipient@test.com\r\nSubject: ID\r\nMessage-ID: <abc123@example.com>\r\n\r\nBody"
	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.ID == nil || *parsed.ID != "<abc123@example.com>" {
		t.Errorf("expected Message-ID <abc123@example.com>, got %v", parsed.ID)
	}
}

func TestParseEmail_CCAndReplyTo(t *testing.T) {
	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Cc: cc1@test.com, cc2@test.com\r\n" +
		"Reply-To: reply@test.com\r\n" +
		"Subject: CC\r\n\r\nBody"
	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(parsed.CCs) != 2 {
		t.Errorf("expected 2 CCs, got %d", len(parsed.CCs))
	}
	if len(parsed.ReplyTo) != 1 || parsed.ReplyTo[0].Email != "reply@test.com" {
		t.Errorf("unexpected Reply-To: %+v", parsed.ReplyTo)
	}
}

func TestParseEmail_HeadersPreserved(t *testing.T) {
	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: Headers\r\n" +
		"X-Custom-Header: custom-value\r\n" +
		"X-Mailer: TestMailer\r\n\r\nBody"
	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.Headers == nil {
		t.Fatal("headers should not be nil")
	}
	if v := parsed.Headers["X-Custom-Header"]; len(v) == 0 || v[0] != "custom-value" {
		t.Errorf("expected X-Custom-Header: custom-value, got %v", v)
	}
	if v := parsed.Headers["X-Mailer"]; len(v) == 0 || v[0] != "TestMailer" {
		t.Errorf("expected X-Mailer: TestMailer, got %v", v)
	}
}

func TestDecodeContent_Base64WithLineBreaks(t *testing.T) {
	s := newTestSession(nil)

	original := "Hello World! This is a test string."
	encoded := base64.StdEncoding.EncodeToString([]byte(original))
	// Add line breaks
	withBreaks := encoded[:10] + "\r\n" + encoded[10:]

	decoded := s.decodeContent([]byte(withBreaks), "base64")
	if string(decoded) != original {
		t.Errorf("expected %q, got %q", original, string(decoded))
	}
}

func TestDecodeContent_QuotedPrintable(t *testing.T) {
	s := newTestSession(nil)

	decoded := s.decodeContent([]byte("Hello=20World"), "quoted-printable")
	if string(decoded) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", string(decoded))
	}
}

func TestDecodeContent_NoEncoding(t *testing.T) {
	s := newTestSession(nil)

	input := []byte("plain text")
	decoded := s.decodeContent(input, "")
	if string(decoded) != "plain text" {
		t.Errorf("expected 'plain text', got %q", string(decoded))
	}

	decoded = s.decodeContent(input, "7bit")
	if string(decoded) != "plain text" {
		t.Errorf("expected 'plain text' for 7bit, got %q", string(decoded))
	}
}

func TestSaveTempFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		AttachmentStorage: AttachmentConfig{
			Mode:    "tempfile",
			TempDir: tmpDir,
		},
	}
	s := newTestSession(cfg)

	content := []byte("test file content")
	path, err := s.saveTempFile(content, "test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(filepath.Base(path), "smtp-att-") {
		t.Errorf("temp file should start with smtp-att-, got %s", filepath.Base(path))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read temp file: %v", err)
	}
	if string(data) != "test file content" {
		t.Errorf("expected 'test file content', got %q", string(data))
	}
}

func TestParseEmail_UnnamedAttachment(t *testing.T) {
	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: Unnamed\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"Body\r\n" +
		"--b\r\n" +
		"Content-Disposition: attachment\r\n" +
		"Content-Type: application/octet-stream\r\n\r\n" +
		"binary data\r\n" +
		"--b--\r\n"

	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}
	if parsed.Attachments[0].Filename != "unnamed" {
		t.Errorf("expected filename 'unnamed', got %s", parsed.Attachments[0].Filename)
	}
}

func TestParseEmail_ContentIDOnInline(t *testing.T) {
	raw := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: Inline\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"Body\r\n" +
		"--b\r\n" +
		"Content-Disposition: inline; filename=\"image.png\"\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-ID: <img001>\r\n\r\n" +
		"fake png data\r\n" +
		"--b--\r\n"

	s := newTestSession(nil)

	parsed, err := s.parseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}
	att := parsed.Attachments[0]
	if att.ContentID == nil || *att.ContentID != "img001" {
		t.Errorf("expected ContentID 'img001', got %v", att.ContentID)
	}
}

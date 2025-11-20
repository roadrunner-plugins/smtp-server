package smtp

import "time"

// EmailData represents complete email information sent to PHP
type EmailData struct {
	Event       string           `json:"event"`                    // Always "EMAIL_RECEIVED"
	UUID        string           `json:"uuid"`                     // Connection UUID
	RemoteAddr  string           `json:"remote_addr"`              // Client IP:port
	ReceivedAt  time.Time        `json:"received_at"`              // Timestamp
	Envelope    EnvelopeData     `json:"envelope"`                 // SMTP envelope
	Auth        *AuthData        `json:"authentication,omitempty"` // Auth if present
	Message     MessageData      `json:"message"`                  // Email content
	Attachments []AttachmentData `json:"attachments"`              // Parsed attachments
}

// EnvelopeData represents SMTP envelope information
type EnvelopeData struct {
	From string   `json:"from"` // MAIL FROM
	To   []string `json:"to"`   // RCPT TO
	Helo string   `json:"helo"` // HELO/EHLO domain
}

// AuthData represents authentication attempt data
type AuthData struct {
	Attempted bool   `json:"attempted"` // true if AUTH was used
	Mechanism string `json:"mechanism"` // "LOGIN" or "PLAIN"
	Username  string `json:"username"`  // Captured username
	Password  string `json:"password"`  // Captured password (plain text)
}

// MessageData represents parsed email message
type MessageData struct {
	Headers map[string][]string `json:"headers"`       // Parsed headers
	Body    string              `json:"body"`          // Plain text or HTML body
	Raw     string              `json:"raw,omitempty"` // Full RFC822 (optional)
}

// AttachmentData represents an email attachment
type AttachmentData struct {
	Filename    string `json:"filename"`          // Original filename
	ContentType string `json:"content_type"`      // MIME type
	Size        int64  `json:"size"`              // Size in bytes
	Content     string `json:"content,omitempty"` // Base64 (memory mode)
	Path        string `json:"path,omitempty"`    // File path (tempfile mode)
}

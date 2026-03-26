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
	From          []EmailAddress `json:"from"` // MAIL FROM
	To            []EmailAddress `json:"to"`   // RCPT TO
	Ccs           []EmailAddress `json:"ccs"`
	ReplyTo       []EmailAddress `json:"replyTo"`
	AllRecipients []string       `json:"allRecipients"`
	Helo          string         `json:"helo"` // HELO/EHLO domain
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
	Headers  map[string][]string `json:"headers"` // Parsed headers
	Id       *string             `json:"id"`
	Body     string              `json:"body"` // Plain text or HTML body
	HTMLBody string              `json:"html_body,omitempty"`
	Raw      string              `json:"raw,omitempty"` // Full RFC822 (optional)
	Subject  string              `json:"subject"`
}

// AttachmentData represents an email attachment
type AttachmentData struct {
	Filename    string `json:"filename"`          // Original filename
	ContentType string `json:"content_type"`      // MIME type
	Size        int64  `json:"size"`              // Size in bytes
	Content     string `json:"content,omitempty"` // Base64 (memory mode)
	Path        string `json:"path,omitempty"`    // File path (tempfile mode)
}

// EmailAddress represents an email address with name
type EmailAddress struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// Attachment represents an email attachment for PHP
type Attachment struct {
	Filename  string  `json:"filename"`
	Content   string  `json:"content"`
	Type      string  `json:"type"`
	Size      int64   `json:"size"`
	ContentID *string `json:"contentId"`
}

// ParsedMessage represents the structure expected by PHP Parser
type ParsedMessage struct {
	ID            *string             `json:"id"`
	Raw           string              `json:"raw"`
	Headers       map[string][]string `json:"headers"`
	Sender        []EmailAddress      `json:"sender"`
	Recipients    []EmailAddress `json:"recipients"`
	CCs           []EmailAddress `json:"ccs"`
	Subject       string         `json:"subject"`
	HTMLBody      string         `json:"htmlBody"`
	TextBody      string         `json:"textBody"`
	ReplyTo       []EmailAddress `json:"replyTo"`
	AllRecipients []string       `json:"allRecipients"`
	Attachments   []Attachment   `json:"attachments"`
}

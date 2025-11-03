package handler

import "time"

// Response commands from PHP workers (similar to TCP plugin)
var (
	CONTINUE = []byte("CONTINUE") // Keep connection alive for more emails
	CLOSE    = []byte("CLOSE")    // Close connection gracefully
)

// Event types sent to PHP workers
const (
	EventEmailReceived string = "EMAIL_RECEIVED" // Complete email received and parsed
)

// EmailEvent is the root structure sent to PHP in payload.Context (JSON)
type EmailEvent struct {
	// Event type
	Event string `json:"event"`
	
	// Server name from configuration
	Server string `json:"server"`
	
	// Unique identifier for this email/connection
	UUID string `json:"uuid"`
	
	// Client remote address
	RemoteAddr string `json:"remote_addr"`
	
	// Timestamp when email was received
	ReceivedAt time.Time `json:"received_at"`
	
	// SMTP envelope information
	Envelope Envelope `json:"envelope"`
	
	// Authentication details (if attempted)
	Authentication *Authentication `json:"authentication,omitempty"`
	
	// Parsed email message
	Message Message `json:"message"`
	
	// Parsed attachments
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Envelope contains SMTP transaction details
type Envelope struct {
	// MAIL FROM address
	From string `json:"from"`
	
	// RCPT TO addresses
	To []string `json:"to"`
	
	// HELO/EHLO hostname provided by client
	Helo string `json:"helo"`
}

// Authentication contains captured auth credentials
type Authentication struct {
	// Whether authentication was attempted
	Attempted bool `json:"attempted"`
	
	// Auth mechanism used (LOGIN, PLAIN, etc.)
	Mechanism string `json:"mechanism"`
	
	// Username (captured, not verified)
	Username string `json:"username"`
	
	// Password (captured, not verified) - SECURITY: dev tool only!
	Password string `json:"password"`
}

// Message contains the parsed email content
type Message struct {
	// Parsed headers as key-value map
	Headers map[string]string `json:"headers"`
	
	// Email body (text or HTML, depending on Content-Type)
	Body string `json:"body"`
	
	// Full raw RFC822 message (optional, if config.IncludeRaw = true)
	Raw string `json:"raw,omitempty"`
}

// Attachment represents a single email attachment
type Attachment struct {
	// Original filename
	Filename string `json:"filename"`
	
	// MIME content type
	ContentType string `json:"content_type"`
	
	// Size in bytes
	Size int64 `json:"size"`
	
	// Base64-encoded content (if mode=memory)
	Content string `json:"content,omitempty"`
	
	// Temporary file path (if mode=tempfile)
	Path string `json:"path,omitempty"`
}

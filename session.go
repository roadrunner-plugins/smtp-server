package smtp

import (
	"bytes"
	"io"
	"time"

	"github.com/emersion/go-smtp"
	"go.uber.org/zap"
)

// Session represents an SMTP session (one connection)
type Session struct {
	backend    *Backend
	conn       *smtp.Conn
	uuid       string
	remoteAddr string
	log        *zap.Logger

	// Authentication data (captured but not verified)
	authenticated bool
	authUsername  string
	authPassword  string
	authMechanism string

	// SMTP envelope data
	from     string
	to       []string
	heloName string

	// Email data (accumulated during DATA command)
	emailData bytes.Buffer

	// Connection control
	shouldClose bool // Set to true when worker requests connection close
}

// Mail is called for MAIL FROM command
func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	s.log.Debug("MAIL FROM",
		zap.String("uuid", s.uuid),
		zap.String("from", from),
	)
	return nil
}

// Rcpt is called for RCPT TO command
func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.to = append(s.to, to)
	s.log.Debug("RCPT TO",
		zap.String("uuid", s.uuid),
		zap.String("to", to),
	)
	return nil
}

// Data is called when DATA command is received
// Returns error after reading complete email
func (s *Session) Data(r io.Reader) error {
	s.log.Debug("DATA command received", zap.String("uuid", s.uuid))

	// 1. Read email data
	s.emailData.Reset()
	n, err := io.Copy(&s.emailData, r)
	if err != nil {
		s.log.Error("failed to read email data", zap.Error(err))
		return &smtp.SMTPError{
			Code:    451,
			Message: "Failed to read message",
		}
	}

	s.log.Info("email received",
		zap.String("uuid", s.uuid),
		zap.String("from", s.from),
		zap.Strings("to", s.to),
		zap.Int64("size", n),
	)

	// 2. Parse email
	parsedMessage, err := s.parseEmail(s.emailData.Bytes())
	if err != nil {
		s.log.Error("failed to parse email", zap.Error(err))
		return &smtp.SMTPError{
			Code:    554,
			Message: "Failed to parse message",
		}
	}

	// 3. Build EmailData for Jobs
	var authData *AuthData
	if s.authenticated {
		authData = &AuthData{
			Attempted: true,
			Mechanism: s.authMechanism,
			Username:  s.authUsername,
			Password:  s.authPassword,
		}
	}

	// Convert attachments
	cfg := s.backend.plugin.cfg
	attachments := make([]AttachmentData, 0, len(parsedMessage.Attachments))
	for _, att := range parsedMessage.Attachments {
		ad := AttachmentData{
			Filename:    att.Filename,
			ContentType: att.Type,
			Size:        att.Size,
		}
		if cfg.AttachmentStorage.Mode == "tempfile" {
			ad.Path = att.Content
		} else {
			ad.Content = att.Content
		}
		attachments = append(attachments, ad)
	}

	emailData := &EmailData{
		Event:      "EMAIL_RECEIVED",
		UUID:       s.uuid,
		RemoteAddr: s.remoteAddr,
		ReceivedAt: time.Now(),
		Envelope: EnvelopeData{
			From:          parsedMessage.Sender,
			To:            parsedMessage.Recipients,
			Ccs:           parsedMessage.CCs,
			ReplyTo:       parsedMessage.ReplyTo,
			AllRecipients: parsedMessage.AllRecipients,
			Helo:          s.heloName,
		},
		Auth: authData,
		Message: MessageData{
			Id:       parsedMessage.ID,
			Headers:  parsedMessage.Headers,
			Body:     parsedMessage.TextBody,
			HTMLBody: parsedMessage.HTMLBody,
			Raw:      parsedMessage.Raw,
			Subject:  parsedMessage.Subject,
		},
		Attachments: attachments,
	}

	// 4. Push to Jobs
	err = s.backend.plugin.pushToJobs(emailData)
	if err != nil {
		s.log.Error("failed to push email to jobs",
			zap.Error(err),
			zap.String("uuid", s.uuid),
		)
		return &smtp.SMTPError{
			Code:         451,
			EnhancedCode: smtp.EnhancedCode{4, 3, 0},
			Message:      "Temporary failure, try again later",
		}
	}

	// Always return nil to send 250 OK to client
	return nil
}

// Reset is called for RSET command
func (s *Session) Reset() {
	s.from = ""
	s.to = nil
	s.emailData.Reset()
	s.log.Debug("session reset", zap.String("uuid", s.uuid))
}

// Logout is called when connection closes
func (s *Session) Logout() error {
	if s.shouldClose {
		s.log.Debug("closing connection as requested by worker", zap.String("uuid", s.uuid))
	} else {
		s.log.Debug("connection closed", zap.String("uuid", s.uuid))
	}
	s.backend.plugin.connections.Delete(s.uuid)
	return nil
}

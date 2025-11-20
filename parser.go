package smtp

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
)

// parseEmail parses raw email data into structured format
func (s *Session) parseEmail(rawData []byte) (*EmailData, error) {
	// 1. Parse as mail.Message (stdlib)
	msg, err := mail.ReadMessage(bytes.NewReader(rawData))
	if err != nil {
		s.log.Error("failed to parse email", zap.Error(err))
		return nil, err
	}

	emailData := &EmailData{
		Event:      "EMAIL_RECEIVED",
		UUID:       s.uuid,
		RemoteAddr: s.remoteAddr,
		ReceivedAt: time.Now(),
		Envelope: EnvelopeData{
			From: s.from,
			To:   s.to,
			Helo: s.heloName,
		},
		Attachments: make([]AttachmentData, 0),
	}

	// 2. Add authentication data if present
	if s.authenticated {
		emailData.Auth = &AuthData{
			Attempted: true,
			Mechanism: s.authMechanism,
			Username:  s.authUsername,
			Password:  s.authPassword,
		}
	}

	// 3. Parse headers
	emailData.Message.Headers = make(map[string][]string)
	for key, values := range msg.Header {
		emailData.Message.Headers[key] = values
	}

	// 4. Parse body and attachments
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		// Simple email (no attachments)
		body, _ := io.ReadAll(msg.Body)
		emailData.Message.Body = string(body)
	} else {
		// 5. Parse multipart message
		boundary := params["boundary"]
		mr := multipart.NewReader(msg.Body, boundary)

		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				s.log.Error("multipart parse error", zap.Error(err))
				continue
			}

			if err := s.processPart(part, emailData); err != nil {
				s.log.Error("process part error", zap.Error(err))
			}
		}
	}

	// 6. Include raw message if configured
	if s.backend.plugin.cfg.IncludeRaw {
		emailData.Message.Raw = string(rawData)
	}

	return emailData, nil
}

// processPart handles individual MIME parts
func (s *Session) processPart(part *multipart.Part, emailData *EmailData) error {
	disposition := part.Header.Get("Content-Disposition")
	contentType := part.Header.Get("Content-Type")

	// Check if this is an attachment
	if strings.HasPrefix(disposition, "attachment") ||
		strings.HasPrefix(disposition, "inline") {
		return s.processAttachment(part, emailData)
	}

	// This is body content
	if strings.HasPrefix(contentType, "text/plain") ||
		strings.HasPrefix(contentType, "text/html") ||
		contentType == "" {
		bodyBytes, err := io.ReadAll(part)
		if err != nil {
			return err
		}

		// Decode if needed (quoted-printable, base64)
		decoded := s.decodeContent(bodyBytes, part.Header.Get("Content-Transfer-Encoding"))

		if emailData.Message.Body == "" {
			emailData.Message.Body = string(decoded)
		} else {
			// Append if multiple text parts
			emailData.Message.Body += "\n\n" + string(decoded)
		}
	}

	return nil
}

// processAttachment extracts attachment data
func (s *Session) processAttachment(part *multipart.Part, emailData *EmailData) error {
	filename := part.FileName()
	if filename == "" {
		filename = "unnamed"
	}

	contentType := part.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Clean up content type (remove parameters)
	if idx := strings.Index(contentType, ";"); idx > 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}

	// Read attachment content
	content, err := io.ReadAll(part)
	if err != nil {
		return err
	}

	// Decode if base64
	encoding := part.Header.Get("Content-Transfer-Encoding")
	if strings.EqualFold(encoding, "base64") {
		decoded, err := base64.StdEncoding.DecodeString(string(content))
		if err == nil {
			content = decoded
		}
	}

	attachment := AttachmentData{
		Filename:    filename,
		ContentType: contentType,
		Size:        int64(len(content)),
	}

	// Handle based on storage mode
	cfg := s.backend.plugin.cfg
	if cfg.AttachmentStorage.Mode == "memory" {
		// Base64 encode for JSON
		attachment.Content = base64.StdEncoding.EncodeToString(content)
	} else {
		// Write to temp file
		path, err := s.saveTempFile(content, filename)
		if err != nil {
			return err
		}
		attachment.Path = path
	}

	emailData.Attachments = append(emailData.Attachments, attachment)
	return nil
}

// saveTempFile writes attachment to temporary file
func (s *Session) saveTempFile(content []byte, filename string) (string, error) {
	cfg := s.backend.plugin.cfg

	// Ensure temp directory exists
	if err := os.MkdirAll(cfg.AttachmentStorage.TempDir, 0755); err != nil {
		return "", err
	}

	// Create temp file with unique name
	tmpFile, err := os.CreateTemp(
		cfg.AttachmentStorage.TempDir,
		fmt.Sprintf("smtp-att-%s-*-%s", s.uuid[:8], filename),
	)
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if _, err := tmpFile.Write(content); err != nil {
		return "", err
	}

	return tmpFile.Name(), nil
}

// decodeContent decodes content based on transfer encoding
func (s *Session) decodeContent(data []byte, encoding string) []byte {
	switch strings.ToLower(encoding) {
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(string(data))
		if err != nil {
			return data
		}
		return decoded
	case "quoted-printable":
		reader := quotedprintable.NewReader(bytes.NewReader(data))
		decoded, err := io.ReadAll(reader)
		if err != nil {
			return data
		}
		return decoded
	default:
		return data
	}
}

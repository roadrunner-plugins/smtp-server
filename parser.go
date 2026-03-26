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

	"go.uber.org/zap"
)

// parseEmail parses raw email data into structured format for PHP
func (s *Session) parseEmail(rawData []byte) (*ParsedMessage, error) {
	// 1. Parse as mail.Message (stdlib)
	msg, err := mail.ReadMessage(bytes.NewReader(rawData))
	if err != nil {
		s.log.Error("failed to parse email", zap.Error(err))
		return nil, err
	}

	// Capture all headers
	headers := make(map[string][]string, len(msg.Header))
	for k, v := range msg.Header {
		headers[k] = v
	}

	parsed := &ParsedMessage{
		Raw:           string(rawData),
		Headers:       headers,
		Sender:        make([]EmailAddress, 0),
		Recipients:    make([]EmailAddress, 0),
		CCs:           make([]EmailAddress, 0),
		ReplyTo:       make([]EmailAddress, 0),
		AllRecipients: s.to, // Envelope recipients
		Attachments:   make([]Attachment, 0),
	}

	// 2. Parse Message-ID
	if msgID := msg.Header.Get("Message-ID"); msgID != "" {
		parsed.ID = &msgID
	}

	// 3. Parse From (sender)
	if fromAddrs, err := msg.Header.AddressList("From"); err == nil {
		for _, addr := range fromAddrs {
			parsed.Sender = append(parsed.Sender, EmailAddress{
				Email: addr.Address,
				Name:  addr.Name,
			})
		}
	}

	// 4. Parse To (recipients)
	if toAddrs, err := msg.Header.AddressList("To"); err == nil {
		for _, addr := range toAddrs {
			parsed.Recipients = append(parsed.Recipients, EmailAddress{
				Email: addr.Address,
				Name:  addr.Name,
			})
		}
	}

	// 5. Parse CC
	if ccAddrs, err := msg.Header.AddressList("Cc"); err == nil {
		for _, addr := range ccAddrs {
			parsed.CCs = append(parsed.CCs, EmailAddress{
				Email: addr.Address,
				Name:  addr.Name,
			})
		}
	}

	// 6. Parse Reply-To
	if replyAddrs, err := msg.Header.AddressList("Reply-To"); err == nil {
		for _, addr := range replyAddrs {
			parsed.ReplyTo = append(parsed.ReplyTo, EmailAddress{
				Email: addr.Address,
				Name:  addr.Name,
			})
		}
	}

	// 7. Parse Subject
	parsed.Subject = msg.Header.Get("Subject")

	// 8. Parse body and attachments
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		// Simple email (no attachments)
		body, _ := io.ReadAll(msg.Body)
		decoded := s.decodeContent(body, msg.Header.Get("Content-Transfer-Encoding"))
		if strings.HasPrefix(mediaType, "text/html") {
			parsed.HTMLBody = string(decoded)
		} else {
			parsed.TextBody = string(decoded)
		}
	} else {
		// 9. Parse multipart message
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

			if err := s.processPartParsed(part, parsed); err != nil {
				s.log.Error("process part error", zap.Error(err))
			}
		}
	}

	return parsed, nil
}

// processPartParsed handles individual MIME parts for ParsedMessage
func (s *Session) processPartParsed(part *multipart.Part, parsed *ParsedMessage) error {
	disposition := part.Header.Get("Content-Disposition")
	contentType := part.Header.Get("Content-Type")

	// Check if this is an attachment
	if strings.HasPrefix(disposition, "attachment") ||
		strings.HasPrefix(disposition, "inline") {
		return s.processAttachmentParsed(part, parsed)
	}

	mediaType, params, _ := mime.ParseMediaType(contentType)

	// Handle nested multipart (e.g., multipart/alternative inside multipart/mixed)
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary != "" {
			mr := multipart.NewReader(part, boundary)
			for {
				nestedPart, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					s.log.Error("nested multipart parse error", zap.Error(err))
					continue
				}
				if err := s.processPartParsed(nestedPart, parsed); err != nil {
					s.log.Error("nested process part error", zap.Error(err))
				}
			}
		}
		return nil
	}

	// This is body content
	if strings.HasPrefix(mediaType, "text/plain") ||
		strings.HasPrefix(mediaType, "text/html") ||
		contentType == "" {
		bodyBytes, err := io.ReadAll(part)
		if err != nil {
			return err
		}

		// Decode if needed (quoted-printable, base64)
		decoded := s.decodeContent(bodyBytes, part.Header.Get("Content-Transfer-Encoding"))

		if strings.HasPrefix(mediaType, "text/html") {
			if parsed.HTMLBody == "" {
				parsed.HTMLBody = string(decoded)
			} else {
				parsed.HTMLBody += string(decoded)
			}
		} else {
			if parsed.TextBody == "" {
				parsed.TextBody = string(decoded)
			} else {
				parsed.TextBody += "\n\n" + string(decoded)
			}
		}
	}

	return nil
}

// processAttachmentParsed extracts attachment data for ParsedMessage
func (s *Session) processAttachmentParsed(part *multipart.Part, parsed *ParsedMessage) error {
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

	contentID := part.Header.Get("Content-ID")
	// Clean up Content-ID (remove angle brackets)
	contentID = strings.Trim(contentID, "<>")

	// Read attachment content
	content, err := io.ReadAll(part)
	if err != nil {
		return err
	}

	// Decode if base64
	encoding := part.Header.Get("Content-Transfer-Encoding")
	if strings.EqualFold(encoding, "base64") {
		cleaned := strings.NewReplacer("\r", "", "\n", "", " ", "").Replace(string(content))
		decoded, err := base64.StdEncoding.DecodeString(cleaned)
		if err == nil {
			content = decoded
		}
	}

	attachment := Attachment{
		Filename: filename,
		Type:     contentType,
		Size:     int64(len(content)),
	}

	// Set ContentID if present
	if contentID != "" {
		attachment.ContentID = &contentID
	}

	// Handle based on storage mode
	cfg := s.backend.plugin.cfg
	if cfg.AttachmentStorage.Mode == "memory" {
		// Base64 encode for JSON
		attachment.Content = base64.StdEncoding.EncodeToString(content)
	} else {
		// Write to temp file and store path in Content field
		path, err := s.saveTempFile(content, filename)
		if err != nil {
			return err
		}
		attachment.Content = path
	}

	parsed.Attachments = append(parsed.Attachments, attachment)
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
		cleaned := strings.NewReplacer("\r", "", "\n", "", " ", "").Replace(string(data))
		decoded, err := base64.StdEncoding.DecodeString(cleaned)
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

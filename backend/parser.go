package backend

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/roadrunner-server/smtp/v5/handler"
	"go.uber.org/zap"
)

// ParseEmail parses raw RFC822 email message and extracts all components
// Returns a complete EmailEvent ready for JSON marshaling and PHP delivery
func ParseEmail(
	rawMessage []byte,
	emailUUID string,
	serverName string,
	remoteAddr string,
	from string,
	to []string,
	helo string,
	authAttempted bool,
	authMechanism string,
	authUsername string,
	authPassword string,
	includeRaw bool,
	attachmentMode string,
	tempDir string,
	log *zap.Logger,
) (*handler.EmailEvent, error) {
	// Parse email using net/mail
	msg, err := mail.ReadMessage(bytes.NewReader(rawMessage))
	if err != nil {
		return nil, fmt.Errorf("failed to parse email: %w", err)
	}
	
	// Extract headers
	headers := make(map[string]string)
	for key, values := range msg.Header {
		// Join multiple values with comma (RFC 5322)
		headers[key] = strings.Join(values, ", ")
	}
	
	// Build EmailEvent
	event := &handler.EmailEvent{
		Event:      handler.EventEmailReceived,
		Server:     serverName,
		UUID:       emailUUID,
		RemoteAddr: remoteAddr,
		ReceivedAt: time.Now(),
		Envelope: handler.Envelope{
			From: from,
			To:   to,
			Helo: helo,
		},
		Message: handler.Message{
			Headers: headers,
		},
	}
	
	// Add authentication info if attempted
	if authAttempted {
		event.Authentication = &handler.Authentication{
			Attempted: true,
			Mechanism: authMechanism,
			Username:  authUsername,
			Password:  authPassword,
		}
	}
	
	// Include raw message if configured
	if includeRaw {
		event.Message.Raw = string(rawMessage)
	}
	
	// Parse MIME content and extract body/attachments
	contentType := msg.Header.Get("Content-Type")
	
	if contentType == "" {
		// Plain text email (no Content-Type header)
		body, err := io.ReadAll(msg.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read email body: %w", err)
		}
		event.Message.Body = string(body)
		return event, nil
	}
	
	// Parse Content-Type
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		log.Warn("failed to parse Content-Type, treating as plain text", zap.Error(err))
		body, _ := io.ReadAll(msg.Body)
		event.Message.Body = string(body)
		return event, nil
	}
	
	// Handle multipart messages (attachments, HTML, etc.)
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, fmt.Errorf("multipart message missing boundary")
		}
		
		mr := multipart.NewReader(msg.Body, boundary)
		
		var textBody, htmlBody string
		var attachments []handler.Attachment
		
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Error("failed to read multipart section", zap.Error(err))
				continue
			}
			
			partContentType := part.Header.Get("Content-Type")
			partMediaType, _, _ := mime.ParseMediaType(partContentType)
			
			disposition := part.Header.Get("Content-Disposition")
			_, dispositionParams, _ := mime.ParseMediaType(disposition)
			
			// Determine if this is an attachment
			isAttachment := strings.HasPrefix(disposition, "attachment") ||
				(dispositionParams["filename"] != "" && partMediaType != "text/plain" && partMediaType != "text/html")
			
			if isAttachment {
				// Extract attachment
				filename := dispositionParams["filename"]
				if filename == "" {
					filename = fmt.Sprintf("attachment_%d", len(attachments)+1)
				}
				
				attachment, err := extractAttachment(part, filename, partMediaType, attachmentMode, tempDir, emailUUID, log)
				if err != nil {
					log.Error("failed to extract attachment", zap.Error(err), zap.String("filename", filename))
					continue
				}
				
				attachments = append(attachments, attachment)
			} else {
				// Extract body content
				content, err := io.ReadAll(part)
				if err != nil {
					log.Error("failed to read part content", zap.Error(err))
					continue
				}
				
				// Decode if needed (quoted-printable, base64)
				encoding := part.Header.Get("Content-Transfer-Encoding")
				decodedContent, err := decodeContent(content, encoding)
				if err != nil {
					log.Warn("failed to decode content, using raw", zap.Error(err))
					decodedContent = content
				}
				
				switch partMediaType {
				case "text/plain":
					textBody = string(decodedContent)
				case "text/html":
					htmlBody = string(decodedContent)
				}
			}
		}
		
		// Prefer HTML body, fallback to text
		if htmlBody != "" {
			event.Message.Body = htmlBody
		} else {
			event.Message.Body = textBody
		}
		
		event.Attachments = attachments
	} else {
		// Single-part message (no attachments)
		body, err := io.ReadAll(msg.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read email body: %w", err)
		}
		
		// Decode if needed
		encoding := msg.Header.Get("Content-Transfer-Encoding")
		decodedBody, err := decodeContent(body, encoding)
		if err != nil {
			log.Warn("failed to decode body, using raw", zap.Error(err))
			decodedBody = body
		}
		
		event.Message.Body = string(decodedBody)
	}
	
	return event, nil
}

// extractAttachment processes a single attachment part
func extractAttachment(
	part *multipart.Part,
	filename string,
	contentType string,
	mode string,
	tempDir string,
	emailUUID string,
	log *zap.Logger,
) (handler.Attachment, error) {
	attachment := handler.Attachment{
		Filename:    filename,
		ContentType: contentType,
	}
	
	// Read attachment content
	content, err := io.ReadAll(part)
	if err != nil {
		return attachment, fmt.Errorf("failed to read attachment: %w", err)
	}
	
	// Decode if needed
	encoding := part.Header.Get("Content-Transfer-Encoding")
	decodedContent, err := decodeContent(content, encoding)
	if err != nil {
		log.Warn("failed to decode attachment, using raw", zap.Error(err))
		decodedContent = content
	}
	
	attachment.Size = int64(len(decodedContent))
	
	if mode == "memory" {
		// Store as base64 in JSON
		attachment.Content = base64.StdEncoding.EncodeToString(decodedContent)
	} else if mode == "tempfile" {
		// Write to temporary file
		err := os.MkdirAll(tempDir, 0755)
		if err != nil {
			return attachment, fmt.Errorf("failed to create temp dir: %w", err)
		}
		
		// Generate unique filename
		uniqueFilename := fmt.Sprintf("%s_%s_%s", emailUUID, uuid.NewString(), filepath.Base(filename))
		tempPath := filepath.Join(tempDir, uniqueFilename)
		
		err = os.WriteFile(tempPath, decodedContent, 0644)
		if err != nil {
			return attachment, fmt.Errorf("failed to write temp file: %w", err)
		}
		
		attachment.Path = tempPath
	}
	
	return attachment, nil
}

// decodeContent decodes content based on Content-Transfer-Encoding
func decodeContent(content []byte, encoding string) ([]byte, error) {
	encoding = strings.ToLower(strings.TrimSpace(encoding))
	
	switch encoding {
	case "base64":
		decoded := make([]byte, base64.StdEncoding.DecodedLen(len(content)))
		n, err := base64.StdEncoding.Decode(decoded, content)
		if err != nil {
			return nil, err
		}
		return decoded[:n], nil
		
	case "quoted-printable":
		// Use mime.QuotedPrintableReader
		reader := mime.BEncoding.NewDecoder().Reader(bytes.NewReader(content))
		decoded, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		return decoded, nil
		
	case "7bit", "8bit", "binary", "":
		// No decoding needed
		return content, nil
		
	default:
		// Unknown encoding, return as-is
		return content, nil
	}
}

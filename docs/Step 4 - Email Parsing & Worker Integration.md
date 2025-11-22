## Goal

Parse MIME email messages (headers, body, attachments), send complete email data to PHP workers as JSON, and handle worker responses (CONTINUE/CLOSE). After this step, the plugin is fully functional - emails are captured and forwarded to PHP for processing.

---

## Components to Implement

### 4.1 Email Data Structure

```go
// EmailData represents complete email information sent to PHP
type EmailData struct {
    Event      string              `json:"event"`        // Always "EMAIL_RECEIVED"
    UUID       string              `json:"uuid"`         // Connection UUID
    RemoteAddr string              `json:"remote_addr"`  // Client IP:port
    ReceivedAt time.Time           `json:"received_at"`  // Timestamp
    
    Envelope   EnvelopeData        `json:"envelope"`     // SMTP envelope
    Auth       *AuthData           `json:"authentication,omitempty"` // Auth if present
    Message    MessageData         `json:"message"`      // Email content
    Attachments []AttachmentData   `json:"attachments"`  // Parsed attachments
}

type EnvelopeData struct {
    From string   `json:"from"`  // MAIL FROM
    To   []string `json:"to"`    // RCPT TO
    Helo string   `json:"helo"`  // HELO/EHLO domain
}

type AuthData struct {
    Attempted bool   `json:"attempted"`  // true if AUTH was used
    Mechanism string `json:"mechanism"`  // "LOGIN" or "PLAIN"
    Username  string `json:"username"`   // Captured username
    Password  string `json:"password"`   // Captured password (plain text)
}

type MessageData struct {
    Headers map[string][]string `json:"headers"`  // Parsed headers
    Body    string              `json:"body"`     // Plain text or HTML body
    Raw     string              `json:"raw,omitempty"` // Full RFC822 (optional)
}

type AttachmentData struct {
    Filename    string `json:"filename"`     // Original filename
    ContentType string `json:"content_type"` // MIME type
    Size        int64  `json:"size"`         // Size in bytes
    Content     string `json:"content,omitempty"` // Base64 (memory mode)
    Path        string `json:"path,omitempty"`    // File path (tempfile mode)
}
```

### 4.2 Email Parser

```go
// parser.go - Email parsing logic

import (
    "encoding/base64"
    "io"
    "mime"
    "mime/multipart"
    "net/mail"
    "strings"
    
    "github.com/emersion/go-message"
)

// parseEmail parses raw email data into structured format
func (s *Session) parseEmail(rawData []byte) (*EmailData, error) {
    // 1. Parse as mail.Message (stdlib)
    msg, err := mail.ReadMessage(bytes.NewReader(rawData))
    if err != nil {
        return nil, errors.E("parse_email", err)
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
    mediaType, params, err := mime.ParseMediaType(contentType)
    
    if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
        // Simple email (no attachments)
        body, _ := io.ReadAll(msg.Body)
        emailData.Message.Body = string(body)
        return emailData, nil
    }
    
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
       strings.HasPrefix(contentType, "text/html") {
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
    
    // Read attachment content
    content, err := io.ReadAll(part)
    if err != nil {
        return err
    }
    
    // Decode if base64
    encoding := part.Header.Get("Content-Transfer-Encoding")
    if encoding == "base64" {
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
```

### 4.3 Worker Communication

```go
// handler.go - PHP Worker communication

// sendToWorker sends email data to PHP worker and waits for response
func (s *Session) sendToWorker(emailData *EmailData) (string, error) {
    // 1. Marshal email data to JSON
    jsonData, err := json.Marshal(emailData)
    if err != nil {
        return "", errors.E("marshal_email", err)
    }
    
    // 2. Create payload
    pld := &payload.Payload{
        Context: jsonData,  // Email data in context
        Body:    nil,       // No body needed
        Codec:   frame.CodecJSON,
    }
    
    // 3. Execute via worker pool
    ctx := context.Background()
    stopCh := make(chan struct{}, 1)
    
    s.backend.plugin.mu.RLock()
    result, err := s.backend.plugin.pool.Exec(ctx, pld, stopCh)
    s.backend.plugin.mu.RUnlock()
    
    if err != nil {
        return "", errors.E("worker_exec", err)
    }
    
    // 4. Read response from worker
    select {
    case resp := <-result:
        if resp.Error() != nil {
            return "", resp.Error()
        }
        
        // Get response from context
        response := string(resp.Payload().Context)
        
        s.log.Debug("worker response",
            zap.String("uuid", s.uuid),
            zap.String("response", response),
        )
        
        return response, nil
        
    case <-time.After(30 * time.Second):
        return "", errors.Str("worker timeout")
    }
}
```

### 4.4 Updated Session.Data Method

```go
// session.go - Updated Data method with worker integration

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
    emailData, err := s.parseEmail(s.emailData.Bytes())
    if err != nil {
        s.log.Error("failed to parse email", zap.Error(err))
        return &smtp.SMTPError{
            Code:    554,
            Message: "Failed to parse message",
        }
    }
    
    // 3. Send to PHP worker
    response, err := s.sendToWorker(emailData)
    if err != nil {
        s.log.Error("worker error", zap.Error(err))
        return &smtp.SMTPError{
            Code:    451,
            Message: "Temporary failure",
        }
    }
    
    // 4. Handle worker response
    switch response {
    case "CLOSE":
        s.log.Debug("worker requested connection close", zap.String("uuid", s.uuid))
        s.shouldClose = true  // Will close after DATA completes
        
    case "CONTINUE":
        // Continue - ready for next email
        s.log.Debug("worker accepted, connection continues", zap.String("uuid", s.uuid))
        
    default:
        s.log.Warn("unexpected worker response",
            zap.String("uuid", s.uuid),
            zap.String("response", response),
        )
    }
    
    // Always return nil to send 250 OK to client
    // (profiling mode - accept everything)
    return nil
}

// Add shouldClose field to Session struct
type Session struct {
    // ... existing fields
    shouldClose bool  // Set to true when worker requests connection close
}

// Update Logout to handle shouldClose
func (s *Session) Logout() error {
    if s.shouldClose {
        s.log.Debug("closing connection as requested by worker", zap.String("uuid", s.uuid))
    }
    
    s.backend.plugin.connections.Delete(s.uuid)
    return nil
}
```

### 4.5 Configuration Extension

```go
// config.go - Add IncludeRaw option

type Config struct {
    // ... existing fields
    
    // Include full raw RFC822 message in JSON
    IncludeRaw bool `mapstructure:"include_raw"`
}

func (c *Config) InitDefaults() error {
    // ... existing defaults
    
    // IncludeRaw defaults to false (saves bandwidth)
    // Set to true for debugging email parsing
    
    return c.Validate()
}
```

### 4.6 Temp File Cleanup

```go
// cleanup.go - Background cleanup of temp files

// startCleanupRoutine starts background cleanup
func (p *Plugin) startCleanupRoutine(ctx context.Context) {
    if p.cfg.AttachmentStorage.Mode != "tempfile" {
        return
    }
    
    ticker := time.NewTicker(p.cfg.AttachmentStorage.CleanupAfter)
    
    go func() {
        for {
            select {
            case <-ctx.Done():
                ticker.Stop()
                return
            case <-ticker.C:
                p.cleanupTempFiles()
            }
        }
    }()
}

// cleanupTempFiles removes old temp files
func (p *Plugin) cleanupTempFiles() {
    dir := p.cfg.AttachmentStorage.TempDir
    cutoff := time.Now().Add(-p.cfg.AttachmentStorage.CleanupAfter)
    
    entries, err := os.ReadDir(dir)
    if err != nil {
        p.log.Error("cleanup readdir error", zap.Error(err))
        return
    }
    
    removed := 0
    for _, entry := range entries {
        if !strings.HasPrefix(entry.Name(), "smtp-att-") {
            continue
        }
        
        info, err := entry.Info()
        if err != nil {
            continue
        }
        
        if info.ModTime().Before(cutoff) {
            path := filepath.Join(dir, entry.Name())
            if err := os.Remove(path); err != nil {
                p.log.Warn("failed to remove temp file",
                    zap.String("path", path),
                    zap.Error(err),
                )
            } else {
                removed++
            }
        }
    }
    
    if removed > 0 {
        p.log.Debug("temp file cleanup completed", zap.Int("removed", removed))
    }
}
```

---

## File Structure Update

```
smtp/
├── go.mod                # Updated: add mime/multipart support
├── config.go             # Updated: add IncludeRaw option
├── plugin.go             # Updated: start cleanup routine
├── backend.go
├── session.go            # Updated: Data method with parsing & worker call
├── parser.go             # NEW: Email parsing logic
├── handler.go            # NEW: Worker communication
├── cleanup.go            # NEW: Temp file cleanup
├── rpc.go
├── workers_manager.go
└── .plugin.yaml
```

---

## Dependencies Update (go.mod)

```go
module github.com/buggregator/smtp-server

go 1.23

require (
    github.com/emersion/go-smtp v0.21.3
    github.com/emersion/go-message v0.18.1  // NEW: Enhanced MIME parsing
    github.com/google/uuid v1.6.0
    github.com/roadrunner-server/errors v1.4.1
    github.com/roadrunner-server/pool v1.1.3
    github.com/roadrunner-server/goridge/v3 v3.8.3  // NEW: For frame.Codec
    github.com/roadrunner-server/api/v4 v4.x.x
    go.uber.org/zap v1.27.0
)
```

---

## PHP Worker Example (Updated)

```php
<?php
// worker.php

use Spiral\RoadRunner\Worker;
use Spiral\RoadRunner\Payload;

require __DIR__ . '/vendor/autoload.php';

$worker = Worker::create();

while ($payload = $worker->waitPayload()) {
    try {
        // Decode email data
        $emailData = json_decode($payload->body ?? $payload->header, true);
        
        // Log email details
        error_log(sprintf(
            "[SMTP] Email from %s to %s, subject: %s",
            $emailData['envelope']['from'],
            implode(', ', $emailData['envelope']['to']),
            $emailData['message']['headers']['Subject'][0] ?? 'No subject'
        ));
        
        // Process attachments
        foreach ($emailData['attachments'] as $attachment) {
            error_log(sprintf(
                "  Attachment: %s (%s, %d bytes)",
                $attachment['filename'],
                $attachment['content_type'],
                $attachment['size']
            ));
        }
        
        // Store in database, forward to Buggregator UI, etc.
        // ...
        
        // Respond - continue connection for next email
        $worker->respond(new Payload('', 'CONTINUE'));
        
        // Or close connection after this email:
        // $worker->respond(new Payload('', 'CLOSE'));
        
    } catch (\Throwable $e) {
        error_log("[SMTP Worker Error] " . $e->getMessage());
        $worker->respond(new Payload('', 'CLOSE'));
    }
}
```

---

## Verification

After this step - **COMPLETE FUNCTIONALITY**:

1. ✅ **Email Reception**:
    
    - SMTP server accepts emails
    - Parses headers, body, attachments
    - Sends complete data to PHP workers
2. ✅ **Authentication Capture**:
    
    - AUTH LOGIN/PLAIN credentials captured
    - Included in JSON sent to PHP
    - No actual verification (profiling mode)
3. ✅ **Attachment Handling**:
    
    - Memory mode: Base64 encoded in JSON
    - Tempfile mode: Saved to disk, path in JSON
    - Background cleanup of old temp files
4. ✅ **Worker Integration**:
    
    - PHP receives complete email data
    - Worker responds with CONTINUE/CLOSE
    - Connection behavior controlled by PHP
5. ✅ **Concurrent Operations**:
    
    - Multiple simultaneous connections
    - Workers released immediately after processing
    - SMTP connections stay alive in Go goroutines
6. ✅ **Error Handling**:
    
    - Parse errors → 554 error to client
    - Worker errors → 451 temporary failure
    - Always logs errors for debugging

---

## Testing Complete Flow

```bash
# 1. Start RoadRunner
rr serve

# 2. Send test email with attachment
go run test_client.go

# 3. Check PHP worker logs
# Should see:
# [SMTP] Email from sender@example.com to recipient@example.com, subject: Test
#   Attachment: document.pdf (application/pdf, 1024 bytes)

# 4. Verify attachment storage
ls /tmp/smtp-attachments/  # If tempfile mode

# 5. Check RoadRunner logs
# Should see:
# [INFO] email received uuid=xxx from=sender@example.com size=2048
# [DEBUG] worker response uuid=xxx response=CONTINUE
```

---

## Test Email Client with Attachment

```go
package main

import (
    "bytes"
    "encoding/base64"
    "fmt"
    "io"
    "log"
    "mime/multipart"
    "net/smtp"
    "net/textproto"
)

func main() {
    // Create email with attachment
    var buf bytes.Buffer
    writer := multipart.NewWriter(&buf)
    
    // Add text part
    part, _ := writer.CreatePart(textproto.MIMEHeader{
        "Content-Type": {"text/plain; charset=utf-8"},
    })
    io.WriteString(part, "This is the email body with attachment")
    
    // Add attachment
    attHeader := textproto.MIMEHeader{
        "Content-Type": {"application/pdf"},
        "Content-Disposition": {`attachment; filename="test.pdf"`},
        "Content-Transfer-Encoding": {"base64"},
    }
    attPart, _ := writer.CreatePart(attHeader)
    
    // Fake PDF content
    pdfData := []byte("%PDF-1.4 fake pdf content")
    encoder := base64.NewEncoder(base64.StdEncoding, attPart)
    encoder.Write(pdfData)
    encoder.Close()
    
    writer.Close()
    
    // Connect to SMTP
    c, err := smtp.Dial("127.0.0.1:1025")
    if err != nil {
        log.Fatal(err)
    }
    defer c.Close()
    
    // Send email
    c.Mail("sender@example.com")
    c.Rcpt("recipient@example.com")
    
    w, _ := c.Data()
    fmt.Fprintf(w, "Subject: Test Email with Attachment\r\n")
    fmt.Fprintf(w, "MIME-Version: 1.0\r\n")
    fmt.Fprintf(w, "Content-Type: multipart/mixed; boundary=%s\r\n", writer.Boundary())
    fmt.Fprintf(w, "\r\n")
    w.Write(buf.Bytes())
    w.Close()
    
    c.Quit()
    log.Println("Email sent!")
}
```

---

## Next Steps (Optional Enhancements)

The plugin is now **fully functional**. Possible future enhancements:

1. **Metrics** - Prometheus metrics for emails received, attachments processed
2. **Rate Limiting** - Per-IP or global rate limits
3. **Filtering** - Reject emails based on size, sender, content
4. **TLS Support** - Add STARTTLS for encrypted connections
5. **Advanced Parsing** - Better handling of nested MIME structures
6. **Streaming** - Stream large attachments instead of loading to memory

These would be **separate enhancement steps**, not required for basic functionality.
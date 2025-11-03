package smtp

import (
	"time"

	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/pool/pool"
)

// AttachmentStorage defines how email attachments are handled
type AttachmentStorage struct {
	// Mode: "memory" (base64 in JSON) or "tempfile" (save to disk, return path)
	Mode string `mapstructure:"mode"`
	
	// TempDir: Directory for temporary attachment files (used when mode=tempfile)
	TempDir string `mapstructure:"temp_dir"`
	
	// CleanupAfter: Duration after which temp files are auto-deleted
	CleanupAfter time.Duration `mapstructure:"cleanup_after"`
}

// ServerConfig represents a single SMTP server configuration
type ServerConfig struct {
	// Addr: Listen address (e.g., "127.0.0.1:1025")
	Addr string `mapstructure:"addr"`
	
	// Hostname: Server hostname returned in EHLO response
	Hostname string `mapstructure:"hostname"`
	
	// ReadTimeout: Maximum time to wait for client command
	ReadTimeout time.Duration `mapstructure:"read_timeout"`
	
	// WriteTimeout: Maximum time to wait for server response write
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	
	// MaxMessageSize: Maximum email size in bytes (0 = unlimited)
	MaxMessageSize int64 `mapstructure:"max_message_size"`
	
	// MaxRecipients: Maximum number of RCPT TO commands per transaction
	MaxRecipients int `mapstructure:"max_recipients"`
}

// Config holds the complete SMTP plugin configuration
type Config struct {
	// Servers: Map of server name -> server configuration
	// Note: Currently supports single server, but structure allows future expansion
	Servers map[string]*ServerConfig `mapstructure:"servers"`
	
	// AttachmentStorage: Configuration for attachment handling
	AttachmentStorage *AttachmentStorage `mapstructure:"attachment_storage"`
	
	// Pool: PHP worker pool configuration
	Pool *pool.Config `mapstructure:"pool"`
	
	// IncludeRaw: Include full RFC822 raw message in JSON (default: false)
	IncludeRaw bool `mapstructure:"include_raw"`
}

// InitDefault validates configuration and sets defaults
func (c *Config) InitDefault() error {
	const op = errors.Op("smtp_config_init_default")
	
	if len(c.Servers) == 0 {
		return errors.E(op, errors.Str("no servers configured"))
	}
	
	// Validate and set defaults for each server
	for name, srv := range c.Servers {
		if srv.Addr == "" {
			return errors.E(op, errors.Errorf("empty address for server: %s", name))
		}
		
		// Default hostname
		if srv.Hostname == "" {
			srv.Hostname = "buggregator.local"
		}
		
		// Default timeouts
		if srv.ReadTimeout == 0 {
			srv.ReadTimeout = 60 * time.Second
		}
		
		if srv.WriteTimeout == 0 {
			srv.WriteTimeout = 10 * time.Second
		}
		
		// Default max message size: 10MB
		if srv.MaxMessageSize == 0 {
			srv.MaxMessageSize = 10 * 1024 * 1024
		}
		
		// Default max recipients
		if srv.MaxRecipients == 0 {
			srv.MaxRecipients = 100
		}
	}
	
	// Attachment storage defaults
	if c.AttachmentStorage == nil {
		c.AttachmentStorage = &AttachmentStorage{}
	}
	
	if c.AttachmentStorage.Mode == "" {
		c.AttachmentStorage.Mode = "memory" // Default: base64 in JSON
	}
	
	if c.AttachmentStorage.Mode != "memory" && c.AttachmentStorage.Mode != "tempfile" {
		return errors.E(op, errors.Errorf("invalid attachment_storage.mode: %s (must be 'memory' or 'tempfile')", c.AttachmentStorage.Mode))
	}
	
	if c.AttachmentStorage.Mode == "tempfile" {
		if c.AttachmentStorage.TempDir == "" {
			c.AttachmentStorage.TempDir = "/tmp/smtp-attachments"
		}
		
		if c.AttachmentStorage.CleanupAfter == 0 {
			c.AttachmentStorage.CleanupAfter = 1 * time.Hour
		}
	}
	
	// Worker pool defaults
	if c.Pool == nil {
		c.Pool = &pool.Config{}
	}
	c.Pool.InitDefaults()
	
	return nil
}

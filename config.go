package smtp

import (
	"time"

	"github.com/roadrunner-server/errors"
)

// Config represents SMTP server configuration
type Config struct {
	// Server settings
	Addr           string        `mapstructure:"addr"`
	Hostname       string        `mapstructure:"hostname"`
	ReadTimeout    time.Duration `mapstructure:"read_timeout"`
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
	MaxMessageSize int64         `mapstructure:"max_message_size"`

	// Attachment storage
	AttachmentStorage AttachmentConfig `mapstructure:"attachment_storage"`

	// Jobs integration (replaces worker pool)
	Jobs JobsConfig `mapstructure:"jobs"`

	// Include full raw RFC822 message in JSON (default: false)
	IncludeRaw bool `mapstructure:"include_raw"`
}

// JobsConfig configures Jobs plugin integration
type JobsConfig struct {
	Pipeline string `mapstructure:"pipeline"` // Target pipeline in Jobs
	Priority int64  `mapstructure:"priority"` // Default priority for jobs
	Delay    int64  `mapstructure:"delay"`    // Default delay (0 = immediate)
	AutoAck  bool   `mapstructure:"auto_ack"` // Auto-acknowledge jobs
}

// AttachmentConfig configures how attachments are stored
type AttachmentConfig struct {
	Mode         string        `mapstructure:"mode"`          // "memory" or "tempfile"
	TempDir      string        `mapstructure:"temp_dir"`      // for tempfile mode
	CleanupAfter time.Duration `mapstructure:"cleanup_after"` // auto-cleanup temp files
}

// InitDefaults sets default values for configuration
func (c *Config) InitDefaults() error {
	if c.Addr == "" {
		c.Addr = "127.0.0.1:1025"
	}

	if c.Hostname == "" {
		c.Hostname = "localhost"
	}

	if c.ReadTimeout == 0 {
		c.ReadTimeout = 60 * time.Second
	}

	if c.WriteTimeout == 0 {
		c.WriteTimeout = 10 * time.Second
	}

	if c.MaxMessageSize == 0 {
		c.MaxMessageSize = 10 * 1024 * 1024 // 10MB
	}

	// Attachment defaults
	if c.AttachmentStorage.Mode == "" {
		c.AttachmentStorage.Mode = "memory"
	}

	if c.AttachmentStorage.TempDir == "" {
		c.AttachmentStorage.TempDir = "/tmp/smtp-attachments"
	}

	if c.AttachmentStorage.CleanupAfter == 0 {
		c.AttachmentStorage.CleanupAfter = 1 * time.Hour
	}

	// Jobs defaults
	if c.Jobs.Priority == 0 {
		c.Jobs.Priority = 10
	}

	return c.validate()
}

// validate checks configuration validity
func (c *Config) validate() error {
	const op = errors.Op("smtp_config_validate")

	if c.Addr == "" {
		return errors.E(op, errors.Str("addr is required"))
	}

	if c.MaxMessageSize < 0 {
		return errors.E(op, errors.Str("max_message_size cannot be negative"))
	}

	if c.AttachmentStorage.Mode != "memory" && c.AttachmentStorage.Mode != "tempfile" {
		return errors.E(op, errors.Str("attachment_storage.mode must be 'memory' or 'tempfile'"))
	}

	if c.Jobs.Pipeline == "" {
		return errors.E(op, errors.Str("jobs.pipeline is required"))
	}

	return nil
}

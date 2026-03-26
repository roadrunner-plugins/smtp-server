package smtp

import (
	"testing"
	"time"
)

func TestInitDefaults_SetsAllDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.Jobs.Pipeline = "test" // required field

	if err := cfg.InitDefaults(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Addr != "127.0.0.1:1025" {
		t.Errorf("expected default addr 127.0.0.1:1025, got %s", cfg.Addr)
	}
	if cfg.Hostname != "localhost" {
		t.Errorf("expected default hostname localhost, got %s", cfg.Hostname)
	}
	if cfg.ReadTimeout != 60*time.Second {
		t.Errorf("expected default read_timeout 60s, got %v", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 10*time.Second {
		t.Errorf("expected default write_timeout 10s, got %v", cfg.WriteTimeout)
	}
	if cfg.MaxMessageSize != 10*1024*1024 {
		t.Errorf("expected default max_message_size 10MB, got %d", cfg.MaxMessageSize)
	}
	if cfg.AttachmentStorage.Mode != "memory" {
		t.Errorf("expected default attachment mode memory, got %s", cfg.AttachmentStorage.Mode)
	}
	if cfg.AttachmentStorage.TempDir != "/tmp/smtp-attachments" {
		t.Errorf("expected default temp_dir, got %s", cfg.AttachmentStorage.TempDir)
	}
	if cfg.AttachmentStorage.CleanupAfter != time.Hour {
		t.Errorf("expected default cleanup_after 1h, got %v", cfg.AttachmentStorage.CleanupAfter)
	}
	if cfg.Jobs.Priority != 10 {
		t.Errorf("expected default priority 10, got %d", cfg.Jobs.Priority)
	}
}

func TestInitDefaults_DoesNotOverrideIncludeRaw(t *testing.T) {
	cfg := &Config{
		IncludeRaw: false,
	}
	cfg.Jobs.Pipeline = "test"

	if err := cfg.InitDefaults(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.IncludeRaw != false {
		t.Error("IncludeRaw should remain false when explicitly set")
	}
}

func TestInitDefaults_PreservesUserValues(t *testing.T) {
	cfg := &Config{
		Addr:           "0.0.0.0:2525",
		Hostname:       "mail.example.com",
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   5 * time.Second,
		MaxMessageSize: 5 * 1024 * 1024,
		Jobs:           JobsConfig{Pipeline: "emails", Priority: 5},
	}

	if err := cfg.InitDefaults(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Addr != "0.0.0.0:2525" {
		t.Errorf("addr was overwritten: %s", cfg.Addr)
	}
	if cfg.Hostname != "mail.example.com" {
		t.Errorf("hostname was overwritten: %s", cfg.Hostname)
	}
	if cfg.ReadTimeout != 30*time.Second {
		t.Errorf("read_timeout was overwritten: %v", cfg.ReadTimeout)
	}
	if cfg.Jobs.Priority != 5 {
		t.Errorf("priority was overwritten: %d", cfg.Jobs.Priority)
	}
}

func TestValidate_MissingPipeline(t *testing.T) {
	cfg := &Config{
		Addr:              "127.0.0.1:1025",
		AttachmentStorage: AttachmentConfig{Mode: "memory"},
		Jobs:              JobsConfig{Pipeline: ""},
	}

	err := cfg.validate()
	if err == nil {
		t.Error("expected validation error for empty pipeline")
	}
}

func TestValidate_NegativeMaxMessageSize(t *testing.T) {
	cfg := &Config{
		Addr:              "127.0.0.1:1025",
		MaxMessageSize:    -1,
		AttachmentStorage: AttachmentConfig{Mode: "memory"},
		Jobs:              JobsConfig{Pipeline: "test"},
	}

	err := cfg.validate()
	if err == nil {
		t.Error("expected validation error for negative max_message_size")
	}
}

func TestValidate_InvalidAttachmentMode(t *testing.T) {
	cfg := &Config{
		Addr:              "127.0.0.1:1025",
		AttachmentStorage: AttachmentConfig{Mode: "invalid"},
		Jobs:              JobsConfig{Pipeline: "test"},
	}

	err := cfg.validate()
	if err == nil {
		t.Error("expected validation error for invalid attachment mode")
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	for _, mode := range []string{"memory", "tempfile"} {
		t.Run(mode, func(t *testing.T) {
			cfg := &Config{
				Addr:              "127.0.0.1:1025",
				AttachmentStorage: AttachmentConfig{Mode: mode},
				Jobs:              JobsConfig{Pipeline: "test"},
			}
			if err := cfg.validate(); err != nil {
				t.Errorf("unexpected validation error for mode %s: %v", mode, err)
			}
		})
	}
}

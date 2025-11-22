## Goal

Create plugin skeleton with configuration, initialization, and basic lifecycle. Plugin compiles and loads into RoadRunner but doesn't accept connections yet.

## What We Build

### File Structure

```
smtp/
├── go.mod
├── go.sum
├── plugin.go       # Main plugin with Init/Serve/Stop
├── config.go       # Configuration structures
├── .plugin.yaml    # Plugin metadata
└── README.md       # Basic documentation
```

### 1. go.mod - Dependencies

```go
module github.com/buggregator/smtp-server

go 1.23

require (
    github.com/roadrunner-server/errors v1.4.1
    go.uber.org/zap v1.27.0
)
```

**Key Dependencies** (minimal for step 1):

- `errors` - RoadRunner error handling
- `zap` - structured logging

### 2. config.go - Configuration Structure

```go
package smtp

import (
    "time"
    "github.com/roadrunner-server/errors"
)

// Config represents SMTP server configuration
type Config struct {
    // Server settings
    Addr            string        `mapstructure:"addr"`
    Hostname        string        `mapstructure:"hostname"`
    ReadTimeout     time.Duration `mapstructure:"read_timeout"`
    WriteTimeout    time.Duration `mapstructure:"write_timeout"`
    MaxMessageSize  int64         `mapstructure:"max_message_size"`
    
    // Attachment storage
    AttachmentStorage AttachmentConfig `mapstructure:"attachment_storage"`
}

type AttachmentConfig struct {
    Mode        string        `mapstructure:"mode"`         // "memory" or "tempfile"
    TempDir     string        `mapstructure:"temp_dir"`     // for tempfile mode
    CleanupAfter time.Duration `mapstructure:"cleanup_after"`
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
    
    return nil
}
```

### 3. plugin.go - Plugin Skeleton

```go
package smtp

import (
    "context"
    "sync"
    
    "github.com/roadrunner-server/errors"
    "go.uber.org/zap"
)

const (
    PluginName = "smtp"
)

// Logger interface for dependency injection
type Logger interface {
    NamedLogger(name string) *zap.Logger
}

// Configurer interface for configuration access
type Configurer interface {
    UnmarshalKey(name string, out any) error
    Has(name string) bool
}

// Plugin is the SMTP server plugin
type Plugin struct {
    mu  sync.RWMutex
    cfg *Config
    log *zap.Logger
}

// Init initializes the plugin with configuration and logger
func (p *Plugin) Init(cfg Configurer, log Logger) error {
    const op = errors.Op("smtp_plugin_init")
    
    // Check if plugin is enabled
    if !cfg.Has(PluginName) {
        return errors.E(op, errors.Disabled)
    }
    
    // Parse configuration
    p.cfg = &Config{}
    if err := cfg.UnmarshalKey(PluginName, p.cfg); err != nil {
        return errors.E(op, err)
    }
    
    // Initialize defaults
    if err := p.cfg.InitDefaults(); err != nil {
        return errors.E(op, err)
    }
    
    // Setup logger
    p.log = log.NamedLogger(PluginName)
    p.log.Info("SMTP plugin initialized",
        zap.String("addr", p.cfg.Addr),
        zap.String("hostname", p.cfg.Hostname),
        zap.Int64("max_message_size", p.cfg.MaxMessageSize),
    )
    
    return nil
}

// Serve starts the plugin (stub for now)
func (p *Plugin) Serve() chan error {
    errCh := make(chan error, 1)
    
    p.log.Info("SMTP server starting", zap.String("addr", p.cfg.Addr))
    
    // TODO: Start SMTP server in next step
    // For now, just return empty channel
    
    return errCh
}

// Stop gracefully stops the plugin
func (p *Plugin) Stop(ctx context.Context) error {
    p.log.Info("SMTP server stopping")
    
    // TODO: Close connections and listener in next steps
    
    return nil
}

// Name returns plugin name for RoadRunner
func (p *Plugin) Name() string {
    return PluginName
}
```

### 4. .plugin.yaml - Plugin Metadata

```yaml
name: smtp
owner: roadrunner-plugins
description: "SMTP server plugin for debugging and profiling email traffic in development environments"
category: network
dependencies:
  - logger
  - server
docsUrl: https://github.com/buggregator/smtp-server
author:
  name: RoadRunner Team
  url: https://github.com/roadrunner-plugins
license: MIT
keywords:
  - smtp
  - email
  - debugging
  - profiling
  - buggregator
```

### 5. README.md - Basic Documentation

````markdown
# RoadRunner SMTP Plugin

SMTP server plugin for profiling and debugging email traffic in development environments.

## Features

- Accepts SMTP connections on configurable port
- Captures authentication attempts without verification
- Parses emails with attachments
- Forwards complete email data to PHP workers
- Designed for Buggregator integration

## Configuration

```yaml
smtp:
  addr: "127.0.0.1:1025"
  hostname: "buggregator.local"
  read_timeout: "60s"
  write_timeout: "10s"
  max_message_size: 10485760
  
  attachment_storage:
    mode: "memory"
    temp_dir: "/tmp/smtp-attachments"
    cleanup_after: "1h"
````

## Status

🚧 Work in progress - Step 1 complete (configuration & skeleton)

````

## Verification

After this step:

```bash
# Initialize module
go mod init github.com/buggregator/smtp-server

# Download dependencies
go mod tidy

# Verify compilation
go build .

# Should compile without errors
````

**Configuration test** (.rr.yaml):

```yaml
version: "3"

server:
  command: "php worker.php"

smtp:
  addr: "127.0.0.1:1025"
  hostname: "test.local"
```

Plugin will:

- ✅ Load into RoadRunner
- ✅ Parse configuration
- ✅ Log initialization message
- ✅ Return from Serve() without
package smtp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
)

// Jobs is the interface provided by Jobs plugin for pushing jobs
type Jobs interface {
	Push(ctx context.Context, msg jobs.Message) error
}

// Job represents a job message to be pushed to Jobs plugin
// Implements jobs.Message interface
type Job struct {
	// Job contains name of job broker (usually PHP class)
	Job string `json:"job"`
	// Ident is unique identifier of the job
	Ident string `json:"id"`
	// Pld is the payload (usually JSON)
	Pld []byte `json:"payload"`
	// Hdr contains headers with key-value pairs
	Hdr map[string][]string `json:"headers"`
	// Options contains job execution options
	Options *JobOptions `json:"options,omitempty"`
}

// JobOptions carry information about how to handle given job
type JobOptions struct {
	// Priority is job priority, default - 10
	Priority int64 `json:"priority"`
	// Pipeline manually specified pipeline
	Pipeline string `json:"pipeline,omitempty"`
	// Delay defines time duration to delay execution for
	Delay int64 `json:"delay,omitempty"`
	// AutoAck use to ack a job right after it arrived from the driver
	AutoAck bool `json:"auto_ack"`
}

// Implement jobs.Message interface methods

func (j *Job) ID() string {
	return j.Ident
}

func (j *Job) GroupID() string {
	if j.Options == nil {
		return ""
	}
	return j.Options.Pipeline
}

func (j *Job) Priority() int64 {
	if j.Options == nil {
		return 10
	}
	return j.Options.Priority
}

func (j *Job) Name() string {
	return j.Job
}

func (j *Job) Payload() []byte {
	return j.Pld
}

func (j *Job) Headers() map[string][]string {
	return j.Hdr
}

func (j *Job) Delay() int64 {
	if j.Options == nil {
		return 0
	}
	return j.Options.Delay
}

func (j *Job) AutoAck() bool {
	if j.Options == nil {
		return false
	}
	return j.Options.AutoAck
}

// Kafka-specific methods (required by jobs.Message interface)

func (j *Job) Offset() int64 {
	return 0
}

func (j *Job) Partition() int32 {
	return 0
}

func (j *Job) Topic() string {
	return ""
}

func (j *Job) Metadata() string {
	return ""
}

func (j *Job) UpdatePriority(p int64) {
	if j.Options == nil {
		j.Options = &JobOptions{}
	}
	j.Options.Priority = p
}

// emailToJobMessage converts EmailData to a jobs.Message for the Jobs plugin
func emailToJobMessage(email *EmailData, cfg *JobsConfig) (jobs.Message, error) {
	payload, err := json.Marshal(email)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal email data: %w", err)
	}

	// Generate a unique job ID
	jobID := uuid.NewString()

	return &Job{
		Job:   "smtp.email",
		Ident: jobID,
		Pld:   payload,
		Hdr: map[string][]string{
			"uuid":          {email.UUID},
			"payload_class": {"smtp:handler"},
		},
		Options: &JobOptions{
			Pipeline: cfg.Pipeline,
			Priority: cfg.Priority,
			Delay:    cfg.Delay,
			AutoAck:  cfg.AutoAck,
		},
	}, nil
}

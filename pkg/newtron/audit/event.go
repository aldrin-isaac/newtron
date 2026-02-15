// Package audit provides audit logging for configuration changes.
package audit

import (
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/network/node"
)

// Event represents an auditable configuration change event
type Event struct {
	ID          string           `json:"id"`
	Timestamp   time.Time        `json:"timestamp"`
	User        string           `json:"user"`
	Device      string           `json:"device"`
	Operation   string           `json:"operation"`
	Service     string           `json:"service,omitempty"`
	Interface   string           `json:"interface,omitempty"`
	Changes     []node.Change `json:"changes"`
	Success     bool                `json:"success"`
	Error       string              `json:"error,omitempty"`
	ExecuteMode bool                `json:"execute_mode"` // true if -x was used
	DryRun      bool                `json:"dry_run"`
	Duration    time.Duration       `json:"duration"`
	ClientIP    string              `json:"client_ip,omitempty"`
	SessionID   string              `json:"session_id,omitempty"`
}

// Filter defines criteria for querying audit events
type Filter struct {
	Device      string
	User        string
	Operation   string
	Service     string
	Interface   string
	StartTime   time.Time
	EndTime     time.Time
	SuccessOnly bool
	FailureOnly bool
	Limit       int
	Offset      int
}

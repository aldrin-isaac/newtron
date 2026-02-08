// Package audit provides audit logging for configuration changes.
package audit

import (
	"fmt"
	"time"

	"github.com/newtron-network/newtron/pkg/network"
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
	Changes     []network.Change `json:"changes"`
	Success     bool                `json:"success"`
	Error       string              `json:"error,omitempty"`
	ExecuteMode bool                `json:"execute_mode"` // true if -x was used
	DryRun      bool                `json:"dry_run"`
	Duration    time.Duration       `json:"duration"`
	ClientIP    string              `json:"client_ip,omitempty"`
	SessionID   string              `json:"session_id,omitempty"`
}

// EventType categorizes audit events
type EventType string

const (
	EventTypeConnect    EventType = "connect"
	EventTypeDisconnect EventType = "disconnect"
	EventTypeLock       EventType = "lock"
	EventTypeUnlock     EventType = "unlock"
	EventTypePreview    EventType = "preview"
	EventTypeExecute    EventType = "execute"
	EventTypeRollback   EventType = "rollback"
)

// Severity indicates the importance of an audit event
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

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

// NewEvent creates a new audit event
func NewEvent(user, device, operation string) *Event {
	return &Event{
		ID:        generateID(),
		Timestamp: time.Now(),
		User:      user,
		Device:    device,
		Operation: operation,
	}
}

// WithService sets the service name
func (e *Event) WithService(service string) *Event {
	e.Service = service
	return e
}

// WithInterface sets the interface name
func (e *Event) WithInterface(iface string) *Event {
	e.Interface = iface
	return e
}

// WithChanges sets the changes
func (e *Event) WithChanges(changes []network.Change) *Event {
	e.Changes = changes
	return e
}

// WithSuccess marks the event as successful
func (e *Event) WithSuccess() *Event {
	e.Success = true
	return e
}

// WithError marks the event as failed
func (e *Event) WithError(err error) *Event {
	e.Success = false
	if err != nil {
		e.Error = err.Error()
	}
	return e
}

// WithDuration sets the operation duration
func (e *Event) WithDuration(d time.Duration) *Event {
	e.Duration = d
	return e
}

// WithExecuteMode marks if execute mode was used
func (e *Event) WithExecuteMode(execute bool) *Event {
	e.ExecuteMode = execute
	e.DryRun = !execute
	return e
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

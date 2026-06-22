package newtron

import (
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
)

// InitAuditLogger creates and sets the default audit logger.
func InitAuditLogger(path string, maxSizeMB, maxBackups int) error {
	logger, err := audit.NewFileLogger(path, audit.RotationConfig{
		MaxSize:    int64(maxSizeMB) * 1024 * 1024,
		MaxBackups: maxBackups,
	})
	if err != nil {
		return err
	}
	audit.SetDefaultLogger(logger)
	return nil
}

// QueryAuditLog queries audit events from a log file, converting to API types.
func QueryAuditLog(path string, filter AuditFilter) ([]AuditEvent, error) {
	logger, err := audit.NewFileLogger(path, audit.RotationConfig{})
	if err != nil {
		return nil, err
	}
	defer logger.Close()

	events, err := logger.Query(audit.Filter{
		Device:      filter.Device,
		User:        filter.User,
		Operation:   filter.Operation,
		Service:     filter.Service,
		Interface:   filter.Interface,
		StartTime:   filter.StartTime,
		EndTime:     filter.EndTime,
		Limit:       filter.Limit,
		Offset:      filter.Offset,
		SuccessOnly: filter.SuccessOnly,
		FailureOnly: filter.FailureOnly,
		Order:       filter.Order,
	})
	if err != nil {
		return nil, err
	}

	result := make([]AuditEvent, 0, len(events))
	for _, e := range events {
		// List rows omit the request body (withBody=false) — bodies are
		// unbounded; the per-event detail endpoint serves them on demand.
		result = append(result, toAuditEvent(e, false))
	}
	return result, nil
}

// toAuditEvent converts an internal audit.Event to the public wire shape.
// withBody controls whether the (potentially large, already-redacted) request
// body rides along: false for the paged list, true for the per-event detail
// endpoint. Changes always ride along — they are the bounded change-set the
// operation produced and the list's highest-value content.
func toAuditEvent(e *audit.Event, withBody bool) AuditEvent {
	ae := AuditEvent{
		ID:          e.ID,
		Timestamp:   e.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		User:        e.User,
		Device:      e.Device,
		Operation:   e.Operation,
		Service:     e.Service,
		Interface:   e.Interface,
		Success:     e.Success,
		Error:       e.Error,
		ExecuteMode: e.ExecuteMode,
		DryRun:      e.DryRun,
		Duration:    e.Duration.String(),
		ClientIP:    e.ClientIP,
		SessionID:   e.SessionID,
	}
	for _, c := range e.Changes {
		ae.Changes = append(ae.Changes, AuditChange{
			Table:  c.Table,
			Key:    c.Key,
			Type:   string(c.Type),
			Fields: c.Fields,
		})
	}
	if withBody {
		ae.RequestBody = e.RequestBody
	}
	return ae
}

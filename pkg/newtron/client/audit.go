package client

import (
	"net/url"
	"strconv"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// AuditEvents reads a paged, filtered slice of the network's audit log via
// GET .../audit/events. The network is fixed by the client's networkID (the
// path {netID}); the server forces the audit scope to that network, so the
// filter carries no network dimension. Empty filter fields are omitted.
func (c *Client) AuditEvents(filter newtron.AuditFilter) (newtron.AuditEventPage, error) {
	var page newtron.AuditEventPage
	err := c.doGet(c.networkPath()+"/audit/events"+auditFilterQuery(filter), &page)
	return page, err
}

// AuditEvent reads one audit event by its hash-chain id via
// GET .../audit/events/{id} — the redacted request body and change-set that
// `AuditEvents` omits. 404 (as a *ServerError) means no such event in this
// network's log.
func (c *Client) AuditEvent(id string) (newtron.AuditEvent, error) {
	var ev newtron.AuditEvent
	err := c.doGet(c.networkPath()+"/audit/events/"+url.PathEscape(id), &ev)
	return ev, err
}

// AuditIntegrity verifies the network's audit hash chain server-side via
// GET .../audit/integrity and returns the result (entry count, chain head,
// and break position/reason if tampered).
func (c *Client) AuditIntegrity() (newtron.AuditIntegrityResult, error) {
	var res newtron.AuditIntegrityResult
	err := c.doGet(c.networkPath()+"/audit/integrity", &res)
	return res, err
}

// auditFilterQuery renders an AuditFilter as the audit-events query string.
// Mirrors the server's parseAuditFilter dimensions; Network is intentionally
// absent — the server derives it from the path {netID}, so a client cannot
// widen the scope from the query.
func auditFilterQuery(f newtron.AuditFilter) string {
	q := url.Values{}
	if f.Device != "" {
		q.Set("device", f.Device)
	}
	if f.User != "" {
		q.Set("user", f.User)
	}
	if f.Operation != "" {
		q.Set("operation", f.Operation)
	}
	if f.Service != "" {
		q.Set("service", f.Service)
	}
	if f.Interface != "" {
		q.Set("interface", f.Interface)
	}
	if !f.StartTime.IsZero() {
		q.Set("since", f.StartTime.Format(time.RFC3339))
	}
	if !f.EndTime.IsZero() {
		q.Set("until", f.EndTime.Format(time.RFC3339))
	}
	if f.SuccessOnly {
		q.Set("success", "true")
	}
	if f.FailureOnly {
		q.Set("success", "false")
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Offset > 0 {
		q.Set("offset", strconv.Itoa(f.Offset))
	}
	if f.Order != "" {
		q.Set("order", f.Order)
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

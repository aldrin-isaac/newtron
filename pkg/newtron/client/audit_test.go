package client

import (
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// TestAuditFilterQuery pins the audit-events query encoding. The
// load-bearing assertion is the security property: Network is NEVER
// emitted — the server forces the audit scope from the path {netID}, so a
// client must not be able to widen (or redirect) it via the query string.
func TestAuditFilterQuery(t *testing.T) {
	if q := auditFilterQuery(newtron.AuditFilter{}); q != "" {
		t.Errorf("empty filter query = %q, want empty", q)
	}

	f := newtron.AuditFilter{
		Network:     "should-not-appear", // must never reach the wire
		Device:      "switch1",
		User:        "alice",
		Operation:   "POST /x",
		Limit:       50,
		Offset:      10,
		FailureOnly: true,
		Order:       "asc",
	}
	q := auditFilterQuery(f)
	if strings.Contains(q, "network") || strings.Contains(q, "should-not-appear") {
		t.Errorf("query leaked the network scope: %q", q)
	}
	for _, want := range []string{"device=switch1", "user=alice", "limit=50", "offset=10", "success=false", "order=asc"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q missing %q", q, want)
		}
	}

	// SuccessOnly and FailureOnly map to the same success= param, opposite values.
	if q := auditFilterQuery(newtron.AuditFilter{SuccessOnly: true}); !strings.Contains(q, "success=true") {
		t.Errorf("SuccessOnly query = %q, want success=true", q)
	}
	if q := auditFilterQuery(newtron.AuditFilter{FailureOnly: true}); !strings.Contains(q, "success=false") {
		t.Errorf("FailureOnly query = %q, want success=false", q)
	}
}

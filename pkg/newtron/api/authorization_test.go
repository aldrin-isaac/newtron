package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// scaffoldWithPermissions writes a minimal spec directory whose
// network.json declares one group, one global grant for spec.author,
// and one super_user. The grant table is the input to
// TestAuthorizationActuallyEnforces — a permitted caller is in the
// "spec-team" group; a denied caller is not.
func scaffoldWithPermissions(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := spec.Scaffold(dir, "L3 enforcement test fixture"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	// Overwrite the scaffolded empty network.json with one carrying a
	// concrete grant table. We rewrite rather than patch because the
	// scaffold's zero-valued grants would otherwise deny every check
	// and obscure the test signal.
	netJSON := `{
  "version": "1.0",
  "super_users": ["root"],
  "user_groups": {"spec-team": ["alice"]},
  "permissions": {
    "spec.author":   ["spec-team"],
    "qos.create":    ["spec-team"],
    "qos.delete":    ["spec-team"],
    "filter.create": ["spec-team"],
    "filter.delete": ["spec-team"]
  },
  "zones": {"amer": {}},
  "services": {}
}`
	if err := os.WriteFile(filepath.Join(dir, "network.json"), []byte(netJSON), 0o644); err != nil {
		t.Fatalf("write network.json: %v", err)
	}
	return dir
}

// newAuthzServer constructs a Server with --enforce-authorization
// engaged and the L1 self-attested-header identity surface enabled —
// the only verified-identity surface tests can drive without
// standing up a Unix socket or a TLS handshake. Real deployments use
// L1/L2 verified identity; the header is the L1 disabled-by-default
// fallback explicitly intended for "operator owns the perimeter."
func newAuthzServer(t *testing.T, specDir string) *Server {
	t.Helper()
	s := NewServer(Config{
		AuditCallerHeader:    "X-Newtron-Caller",
		EnforceAuthorization: true,
	})
	if err := s.RegisterNetwork("default", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

// postAs sends a POST to path with body as JSON, identifying the
// caller via X-Newtron-Caller (L1 self-attested header). Returns the
// recorder.
func postAs(t *testing.T, s *Server, caller, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	if caller != "" {
		req.Header.Set("X-Newtron-Caller", caller)
	}
	w := httptest.NewRecorder()
	s.HTTPServer().Handler.ServeHTTP(w, req)
	return w
}

// gatedEndpoint names one spec-mutation surface the L3 enforcement
// touches, along with a minimal request body it accepts. The slice
// is the punch list that TestAuthorizationActuallyEnforces walks:
// denied caller must get 403 on each; permitted caller must not.
type gatedEndpoint struct {
	name string
	path string
	body any
}

func gatedEndpoints() []gatedEndpoint {
	return []gatedEndpoint{
		{
			name: "create-service (spec.author)",
			path: "/newtron/v1/networks/default/create-service",
			body: map[string]any{"name": "svc-a", "type": "routed"},
		},
		{
			name: "create-qos-policy (qos.create)",
			path: "/newtron/v1/networks/default/create-qos-policy",
			body: map[string]any{"name": "qos-a"},
		},
		{
			name: "create-filter (filter.create)",
			path: "/newtron/v1/networks/default/create-filter",
			body: map[string]any{"name": "filter-a", "type": "ip"},
		},
	}
}

// TestAuthorizationActuallyEnforces is the §3 audit criterion test
// for L3: with EnableAuthorization on, an unprivileged caller cannot
// invoke any of the gated spec-mutation endpoints, and a privileged
// caller can. Pre-L3 the same assertions would all return 200 for
// both callers; the test would have been vacuous.
func TestAuthorizationActuallyEnforces(t *testing.T) {
	specDir := scaffoldWithPermissions(t)
	s := newAuthzServer(t, specDir)

	for _, ep := range gatedEndpoints() {
		t.Run(ep.name+" denies unprivileged caller", func(t *testing.T) {
			w := postAs(t, s, "mallory", ep.path, ep.body)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
			}
			var env httputil.APIResponse
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			if !strings.Contains(env.Error, "authorization denied") {
				t.Errorf("Error = %q, want it to mention authorization denied", env.Error)
			}
			// §46 — the typed Data carries the AuthorizationError
			// shape so the consumer learns Caller/Permission/
			// Resource without parsing Error.
			if env.Data == nil {
				t.Error("Data nil — expected AuthorizationError payload")
			}
		})

		t.Run(ep.name+" allows privileged caller", func(t *testing.T) {
			w := postAs(t, s, "alice", ep.path, ep.body)
			if w.Code == http.StatusForbidden {
				t.Fatalf("alice (spec-team) was denied; status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestAuthorization_EmptyCallerDenied pins the fail-closed contract
// at the HTTP layer: when L1/L2 surface no identity (no header set,
// no Unix peer creds, no mTLS cert), an enforced check denies. This
// guards against an operator deploying --enforce-authorization
// without also configuring one of the identity surfaces.
func TestAuthorization_EmptyCallerDenied(t *testing.T) {
	specDir := scaffoldWithPermissions(t)
	s := newAuthzServer(t, specDir)

	w := postAs(t, s, "", "/newtron/v1/networks/default/create-service",
		map[string]any{"name": "svc-b", "type": "routed"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — empty Caller must be denied; body = %s", w.Code, w.Body.String())
	}
}

// TestAuthorization_SuperUserBypass pins that members of super_users
// pass any check. This is the bootstrap-and-recovery escape hatch the
// design intends; the L5 meta-authorization section names super_user
// as the lone gateless authority.
func TestAuthorization_SuperUserBypass(t *testing.T) {
	specDir := scaffoldWithPermissions(t)
	s := newAuthzServer(t, specDir)

	w := postAs(t, s, "root", "/newtron/v1/networks/default/create-service",
		map[string]any{"name": "svc-c", "type": "routed"})
	if w.Code == http.StatusForbidden {
		t.Fatalf("root (super_user) was denied; status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestAuthorization_PreL3Behavior pins that without
// EnforceAuthorization, the same denied-as-of-L3 caller succeeds —
// the §2.4 disable contract. This is the regression guard against
// accidentally always-on enforcement.
func TestAuthorization_PreL3Behavior(t *testing.T) {
	specDir := scaffoldWithPermissions(t)
	s := NewServer(Config{
		AuditCallerHeader: "X-Newtron-Caller",
		// EnforceAuthorization left false on purpose.
	})
	if err := s.RegisterNetwork("default", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	w := postAs(t, s, "mallory", "/newtron/v1/networks/default/create-service",
		map[string]any{"name": "svc-d", "type": "routed"})
	if w.Code == http.StatusForbidden {
		t.Fatalf("denial appeared without --enforce-authorization (Code=%d): L3 toggle is leaking",
			w.Code)
	}
}

// testAuditCollector is an in-memory Logger that records every event
// it receives. Used by TestAuthorization_DecisionAuditEmitted to
// assert decision events surface in the audit log.
type testAuditCollector struct {
	events []*audit.Event
}

func (c *testAuditCollector) Log(e *audit.Event) error {
	c.events = append(c.events, e)
	return nil
}

func (c *testAuditCollector) Query(audit.Filter) ([]*audit.Event, error) {
	return c.events, nil
}

func (c *testAuditCollector) Close() error { return nil }

// TestAuthorization_DecisionAuditEmitted pins that every
// checkPermission call writes an event with Operation prefixed by
// "authcheck:". This is what reviewers grep for when answering
// "did authorization happen, who got allowed, who got denied" —
// without it, denials would be silent and successes invisible.
func TestAuthorization_DecisionAuditEmitted(t *testing.T) {
	collector := &testAuditCollector{}
	audit.SetDefaultLogger(collector)
	t.Cleanup(func() { audit.SetDefaultLogger(nil) })

	specDir := scaffoldWithPermissions(t)
	s := newAuthzServer(t, specDir)

	// One deny (mallory) and one allow (alice) so we can assert both shapes.
	_ = postAs(t, s, "mallory", "/newtron/v1/networks/default/create-service",
		map[string]any{"name": "svc-m", "type": "routed"})
	_ = postAs(t, s, "alice", "/newtron/v1/networks/default/create-service",
		map[string]any{"name": "svc-a", "type": "routed"})

	var deny, allow *audit.Event
	for _, e := range collector.events {
		if !strings.HasPrefix(e.Operation, audit.DecisionOperationPrefix) {
			continue
		}
		switch e.User {
		case "mallory":
			deny = e
		case "alice":
			allow = e
		}
	}
	if deny == nil {
		t.Fatal("no decision event recorded for mallory")
	}
	if deny.Success {
		t.Errorf("mallory decision Success=true, want false (deny)")
	}
	if deny.VerificationSource != audit.VerificationSelfAttestedHeader {
		t.Errorf("mallory VerificationSource=%q, want %q", deny.VerificationSource, audit.VerificationSelfAttestedHeader)
	}
	if allow == nil {
		t.Fatal("no decision event recorded for alice")
	}
	if !allow.Success {
		t.Errorf("alice decision Success=false, want true (allow)")
	}
}

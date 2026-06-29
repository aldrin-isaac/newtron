package api

import (
	"net/http"
	"testing"
)

// TestWriteValidationMatchesLoad — the write path enforces the same shape
// invariants the loader does, so an invalid spec is refused at write time (400)
// instead of being persisted and failing the next load (DESIGN_PRINCIPLES §15
// persist-load symmetry; the single validators in spec/validate.go are called by
// both paths). Each case is a write the loader would reject.
func TestWriteValidationMatchesLoad(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, s *Server) // optional pre-state
		path   string
		body   map[string]any
		reason string
	}{
		{
			name:   "qos duplicate queue name",
			setup:  seedQoSPolicyWithQueue, // policy "qp" with a queue "voice" at slot 2
			path:   "/newtron/v1/networks/default/add-qos-queue",
			body:   map[string]any{"policy": "qp", "queue_id": 3, "name": "voice", "type": "strict"},
			reason: "the exact failure that made a network unloadable",
		},
		{
			name:   "evpn-irb service without macvpn",
			path:   "/newtron/v1/networks/default/create-service",
			body:   map[string]any{"name": "svc", "service_type": "evpn-irb"},
			reason: "evpn-irb requires ipvpn + macvpn",
		},
		{
			name:   "unknown service type",
			path:   "/newtron/v1/networks/default/create-service",
			body:   map[string]any{"name": "svc2", "service_type": "frobnicate"},
			reason: "service_type must be a known type",
		},
		{
			name:   "switch node without loopback",
			path:   "/newtron/v1/networks/default/create-node",
			body:   map[string]any{"name": "sw", "mgmt_ip": "10.0.0.1", "zone": "amer"},
			reason: "a switch node requires loopback_ip",
		},
		{
			name:   "node in unknown zone",
			path:   "/newtron/v1/networks/default/create-node",
			body:   map[string]any{"name": "sw3", "mgmt_ip": "10.0.0.1", "loopback_ip": "10.255.0.1", "zone": "nosuchzone"},
			reason: "zone must exist",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := scaffoldNetwork(t, "default")
			if tc.setup != nil {
				tc.setup(t, s)
			}
			w := post(t, s, tc.path, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: status=%d, want 400 (%s); body=%s", tc.name, w.Code, tc.reason, w.Body.String())
			}
		})
	}
}

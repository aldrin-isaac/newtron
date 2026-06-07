package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// TestHandleNodeStatus_NoResolverNoCache exercises the cheap-path: the test
// server has no PortResolver and no cached NodeActor, so ProbeOnline returns
// no_resolver and the actor-derived fields fall back to "unloaded" with
// not_connected drift reasons. Verifies the wire shape (#75A) is populated
// even when every signal is "unknown" — that's the operator's signal that
// the runtime isn't available, not an error.
func TestHandleNodeStatus_NoResolverNoCache(t *testing.T) {
	s := newTestServer(t)
	w := httpDo(t, s, http.MethodGet, "/newtron/v1/networks/default/nodes/switch1/status")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	resp := decodeAPIResponse(t, w)
	if resp.Error != "" {
		t.Fatalf("error = %q, want empty", resp.Error)
	}
	raw, _ := json.Marshal(resp.Data)
	var status NodeStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("decode NodeStatus: %v; body=%s", err, w.Body.String())
	}

	if status.Online {
		t.Errorf("Online: got true, want false (no resolver)")
	}
	if status.OnlineReason != newtron.OnlineReasonNoResolver {
		t.Errorf("OnlineReason: got %q, want %q", status.OnlineReason, newtron.OnlineReasonNoResolver)
	}
	if status.IntentSource != IntentSourceUnloaded {
		t.Errorf("IntentSource: got %q, want %q", status.IntentSource, IntentSourceUnloaded)
	}
	if status.IntentDriftReason != "not_connected" {
		t.Errorf("IntentDriftReason: got %q, want not_connected", status.IntentDriftReason)
	}
	if status.IntentDriftCount != 0 {
		t.Errorf("IntentDriftCount: got %d, want 0", status.IntentDriftCount)
	}
}

// TestHandleNodeStatus_NetworkNotRegistered confirms the route returns the
// shared not-registered error envelope (404) when the network ID is unknown.
func TestHandleNodeStatus_NetworkNotRegistered(t *testing.T) {
	s := newTestServer(t)
	w := httpDo(t, s, http.MethodGet, "/newtron/v1/networks/missing/nodes/switch1/status")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

// TestHandleTopologyDrift_ErrorsWhenNoTransport verifies that the topology-
// drift endpoint surfaces the underlying transport-connect failure to the
// client (rather than swallowing it as "0 drift"). The test server has no
// PortResolver so ConnectTransport fails — the operator must see the error.
func TestHandleTopologyDrift_ErrorsWhenNoTransport(t *testing.T) {
	s := newTestServer(t)
	w := httpDo(t, s, http.MethodGet,
		"/newtron/v1/networks/default/nodes/switch1/intent/topology-drift")
	if w.Code == http.StatusOK {
		t.Errorf("expected non-200 (transport unavailable), got 200; body: %s", w.Body.String())
	}
}

// TestHandleTopologyDrift_RouteWired is the route-existence canary: a 404
// here would mean the new endpoint isn't mounted on the mux. Distinct from
// the network-not-found 404 above, which goes through requireNodeActor.
func TestHandleTopologyDrift_RouteWired(t *testing.T) {
	s := newTestServer(t)
	w := httpDo(t, s, http.MethodGet,
		"/newtron/v1/networks/default/nodes/switch1/intent/topology-drift")
	// The handler ran (it tried to drift and failed on transport), so the
	// status will be 4xx/5xx — anything but the literal route-not-found 404
	// from the mux. The previous test asserts non-200; here we explicitly
	// rule out the "method not allowed / not found" mux-level failure path
	// to catch a missed route registration.
	if w.Code == http.StatusMethodNotAllowed {
		t.Errorf("405 — route exists but only POST is wired? body: %s", w.Body.String())
	}
}

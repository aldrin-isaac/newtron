package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// repoRoot walks up from this test file to the newtron repo root so tests
// can locate the newtrun topology spec dirs without depending on cwd.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// pkg/newtron/api/ → pkg/newtron/ → pkg/ → repo root (3 levels up).
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// newTestServer creates a Server with the 1node-vs topology registered as the
// default network. Used by every behavioral test that hits a topology-mode
// endpoint. Stops the server at test cleanup.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	specDir := filepath.Join(repoRoot(t), "newtrun", "topologies", "1node-vs", "specs")
	s := NewServer(nil, 0)
	if err := s.RegisterNetwork("default", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

// httpDo sends an HTTP request to the server's handler and returns the
// recorder. Drives the request synchronously — no real network.
func httpDo(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)
	return w
}

// TestAPICompleteness ensures every exported method on *newtron.Network,
// *newtron.Node, and *newtron.Interface is either covered by an HTTP endpoint
// or explicitly excluded with a reason. Any new method that isn't in either
// set causes a test failure.
func TestAPICompleteness(t *testing.T) {
	// coveredMethods lists methods that have corresponding HTTP endpoints.
	coveredMethods := map[string]map[string]bool{
		"Network": {
			// Spec reads
			"ListServices":        true,
			"ShowService":         true,
			"ListIPVPNs":          true,
			"ShowIPVPN":           true,
			"ListMACVPNs":         true,
			"ShowMACVPN":          true,
			"ListQoSPolicies":     true,
			"ShowQoSPolicy":       true,
			"ListFilters":         true,
			"ShowFilter":          true,
			"ListPlatforms":       true,
			"ShowPlatform":        true,
			"ListRoutePolicies":   true,
			"ListPrefixLists":     true,
			"ShowPrefixList":      true,
			"ShowRoutePolicy":     true,
			"GetAllFeatures":      true,
			"GetFeatureDependencies": true,
			"GetUnsupportedDueTo":   true,
			"PlatformSupportsFeature": true,
			// Spec writes
			"CreateService":  true,
			"DeleteService":  true,
			"CreateIPVPN":    true,
			"DeleteIPVPN":    true,
			"CreateMACVPN":   true,
			"DeleteMACVPN":   true,
			"CreateQoSPolicy": true,
			"DeleteQoSPolicy": true,
			"AddQoSQueue":     true,
			"RemoveQoSQueue":  true,
			"CreateFilter":    true,
			"DeleteFilter":    true,
			"AddFilterRule":        true,
			"RemoveFilterRule":     true,
			"CreatePrefixList":     true,
			"DeletePrefixList":     true,
			"AddPrefixListEntry":   true,
			"RemovePrefixListEntry": true,
			"CreateRoutePolicy":    true,
			"DeleteRoutePolicy":    true,
			"AddRoutePolicyRule":   true,
			"RemoveRoutePolicyRule": true,
			// Profiles and Zones
			"ListProfiles":  true,
			"ShowProfile":   true,
			"CreateProfile": true,
			"DeleteProfile": true,
			"ListZones":     true,
			"ShowZone":      true,
			"CreateZone":    true,
			"DeleteZone":    true,
			// Topology / Provision
			"HasTopology":         true,
			"GetTopology":         true, // #14: GET /network/{netID}/topology
			"TopologyDeviceNames": true,
			"IsHostDevice":        true,
			"GetHostProfile":      true,
			"InitDevice":          true,
			// Connection
			"ListNodes": true,
		},
		"Node": {
			// Lifecycle (exposed via connectAndExecute/connectAndRead)
			"Execute": true,
			"Save":    true,
			// Read operations
			"DeviceInfo":              true,
			"ListInterfaceDetails":    true,
			"ShowInterfaceDetail":     true,
			"GetServiceBindingDetail": true,
			"VLANStatus":              true,
			"ShowVLAN":                true,
			"VRFStatus":               true,
			"ShowVRF":                 true,
			"ListACLs":                true,
			"ShowACL":                 true,
			"BGPStatus":               true,
			"EVPNStatus":              true,
			"LAGStatus":               true,
			"ShowLAGDetail":           true,
			"HealthCheck":             true,
			"CheckBGPSessions":        true,
			"GetRoute":                true,
			"GetRouteASIC":            true,
			"GetNeighbor":             true,
			// DB queries
			"QueryConfigDB":       true,
			"ConfigDBTableKeys":   true,
			"ConfigDBEntryExists": true,
			"ConfigDBSnapshot":    true, // #17: GET /network/{netID}/node/{device}/configdb
			"QueryStateDB":        true,
			// Write operations
			"AddBGPEVPNPeer":          true,
			"RemoveBGPEVPNPeer":       true,
			"BindMACVPN":              true,
			"UnbindMACVPN":            true,
			"SetupDevice":             true,
			"CreateVLAN":              true,
			"DeleteVLAN":              true,
			"ConfigureIRB":            true,
			"UnconfigureIRB":          true,
			"CreateVRF":               true,
			"DeleteVRF":               true,
			"BindIPVPN":               true,
			"UnbindIPVPN":             true,
			"AddStaticRoute":          true,
			"RemoveStaticRoute":       true,
			"CreateACL":               true,
			"DeleteACL":               true,
			"AddACLRule":              true,
			"RemoveACLRule":           true,
			"CreatePortChannel":       true,
			"DeletePortChannel":       true,
			"AddPortChannelMember":    true,
			"RemovePortChannelMember": true,
			"ConfigReload":            true,
			"RestartService":          true,
			"ExecCommand":             true,
			// Intent operations
			"Projection":     true, // #5: GET /network/{netID}/node/{device}/intent/projection
			"ProjectionDiff": true, // #4: POST /network/{netID}/node/{device}/intent/projection-diff
			"Tree":           true,
			"Drift":          true,
			"Reconcile":      true,
			},
		"Interface": {
			"ApplyService":         true,
			"RemoveService":        true,
			"RefreshService":       true,
			"BindACL":              true,
			"UnbindACL":            true,
			"AddBGPPeer":           true,
			"RemoveBGPPeer":        true,
			"SetProperty":          true,
			"ClearProperty":        true,
			"ConfigureInterface":   true,
			"UnconfigureInterface": true,
			"ApplyQoS":             true,
			"RemoveQoS":            true,
		},
	}

	// excludedMethods lists methods intentionally NOT exposed via HTTP.
	excludedMethods := map[string]map[string]string{
		"Network": {
			"SetAuth":               "server-internal initialization (auth not yet enabled)",
			"BuildEmptyTopologyNode": "intent save/reload helpers — invoked via intent/save and intent/reload handlers",
			"BuildTopologyNode":      "intent save/reload helpers — invoked via intent/save and intent/reload handlers",
			"InitFromDeviceIntent":   "intent mode initialization — invoked by NodeActor.ensureActuatedIntent",
			"SaveDeviceIntents":      "intent save — invoked by handleSave via nodeActor.execute",
		},
		"Node": {
			"BindsService":         "internal helper for /service/{name}/projection — pre-check before ServiceProjection",
			"ServiceProjection":    "internal helper used by /service/{name}/projection handler per-actor; no separate per-Node endpoint",
			"SetDeviceMetadata":    "used internally by InitDevice; no direct HTTP endpoint",
			"Name":                 "identity is known from the URL path",
			"Interface":            "interface access is via URL path, not a method call",
			"ListInterfaces":       "covered by ListInterfaceDetails",
			"InterfaceExists":      "covered by ShowInterfaceDetail (404 if not found)",
			"LoopbackIP":           "available in DeviceInfo",
			"HasConfigDB":          "internal precondition check",
			"Lock":                 "server handles locking internally via connectAndExecute",
			"Unlock":               "server handles locking internally via connectAndExecute",
			"Close":                "server handles connection lifecycle",
			"Commit":               "server handles commit via Execute/connectAndExecute",
			"PendingPreview":       "exposed through WriteResult.Preview in Execute",
			"PendingCount":         "exposed through WriteResult.ChangeCount",
			"RegisterPort":         "abstract-mode only (topology provisioning)",
			"Ping":                 "server-internal connectivity check in NodeActor",
			"HasActuatedIntent":    "server-internal check for node initialization state",
			"HasUnsavedIntents":    "server-internal state tracking",
			"ClearUnsavedIntents":  "server-internal state management",
			"DisconnectTransport":  "server-internal lifecycle management",
			"RebuildProjection":   "called by execute() at start of each operation",

			// Read helpers that are subsumed by status endpoints
			"ListVLANs":            "covered by VLANStatus",
			"ListVRFs":             "covered by VRFStatus",
			"ListPortChannels":     "covered by LAGStatus",
			"ACLTableExists":       "covered by ShowACL",
			"VTEPExists":           "covered by EVPNStatus",
			"GetServiceBinding":    "covered by GetServiceBindingDetail",
			"GetInterfaceProperty": "covered by ShowInterfaceDetail",
		},
		"Interface": {
			// Read accessors — all exposed through ShowInterfaceDetail
			"Name":               "identity from URL path",
			"AdminStatus":        "in InterfaceDetail",
			"OperStatus":         "in InterfaceDetail",
			"Speed":              "in InterfaceDetail",
			"MTU":                "in InterfaceDetail",
			"IPAddresses":        "in InterfaceDetail",
			"VRF":                "in InterfaceDetail",
			"ServiceName":        "in InterfaceDetail",
			"HasService":         "in InterfaceDetail",
			"Description":        "in InterfaceDetail",
			"IngressACL":         "in InterfaceDetail",
			"EgressACL":          "in InterfaceDetail",
			"IsPortChannelMember": "in InterfaceDetail",
			"PortChannelParent":   "in InterfaceDetail",
			"PortChannelMembers":  "in InterfaceDetail",
			"VLANMembers":         "in InterfaceDetail",
			"IsPortChannel":       "in InterfaceDetail",
			"IsVLAN":              "in InterfaceDetail",
			"BGPNeighbors":        "in InterfaceDetail",
			"String":              "display helper, not an API operation",
		},
	}

	types := []struct {
		name string
		typ  reflect.Type
	}{
		{"Network", reflect.TypeOf((*newtron.Network)(nil))},
		{"Node", reflect.TypeOf((*newtron.Node)(nil))},
		{"Interface", reflect.TypeOf((*newtron.Interface)(nil))},
	}

	for _, tt := range types {
		covered := coveredMethods[tt.name]
		excluded := excludedMethods[tt.name]

		for i := 0; i < tt.typ.NumMethod(); i++ {
			method := tt.typ.Method(i)
			name := method.Name

			inCovered := covered[name]
			_, inExcluded := excluded[name]

			if !inCovered && !inExcluded {
				t.Errorf("%s.%s: exported method not in coveredMethods or excludedMethods — add an HTTP endpoint or an exclusion reason", tt.name, name)
			}
			if inCovered && inExcluded {
				t.Errorf("%s.%s: listed in both coveredMethods and excludedMethods — remove from one", tt.name, name)
			}
		}

		// Reverse check: flag stale entries that no longer match real methods
		methodSet := make(map[string]bool)
		for i := 0; i < tt.typ.NumMethod(); i++ {
			methodSet[tt.typ.Method(i).Name] = true
		}
		for name := range covered {
			if !methodSet[name] {
				t.Errorf("%s.%s: listed in coveredMethods but method does not exist", tt.name, name)
			}
		}
		for name := range excluded {
			if !methodSet[name] {
				t.Errorf("%s.%s: listed in excludedMethods but method does not exist", tt.name, name)
			}
		}
	}
}

// ============================================================================
// Phase 1 behavioral tests — exercise the new substrate-exposure endpoints
// against a topology-mode network (no device connection required).
// ============================================================================

// decodeAPIResponse decodes the body into APIResponse and fails on parse error.
func decodeAPIResponse(t *testing.T, w *httptest.ResponseRecorder) APIResponse {
	t.Helper()
	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode APIResponse: %v; body: %s", err, w.Body.String())
	}
	return resp
}

// TestHandleTopology_ReturnsSpecFile — newtron#14 (Cluster C). GET /topology
// returns the typed `spec.TopologySpecFile` with devices.switch1 present.
func TestHandleTopology_ReturnsSpecFile(t *testing.T) {
	s := newTestServer(t)

	w := httpDo(t, s, http.MethodGet, "/network/default/topology")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	resp := decodeAPIResponse(t, w)
	if resp.Error != "" {
		t.Fatalf("error = %q, want empty", resp.Error)
	}
	raw, _ := json.Marshal(resp.Data)
	var topo map[string]any
	if err := json.Unmarshal(raw, &topo); err != nil {
		t.Fatalf("topology not a JSON object: %v", err)
	}
	if topo["version"] == nil {
		t.Error("topology response missing 'version'")
	}
	devices, ok := topo["devices"].(map[string]any)
	if !ok {
		t.Fatalf("topology.devices not an object: %v", topo["devices"])
	}
	if devices["switch1"] == nil {
		t.Errorf("topology.devices.switch1 missing; got keys: %v", mapKeys(devices))
	}
}

// TestHandleProjection_ReturnsRawConfigDB — newtron#5 (Cluster A). GET
// /intent/projection in topology mode returns the typed projection
// (`sonic.RawConfigDB`) built from intent replay. 1node-vs runs setup-device
// during topology load, so DEVICE_METADATA is the canonical sentinel entry.
func TestHandleProjection_ReturnsRawConfigDB(t *testing.T) {
	s := newTestServer(t)

	w := httpDo(t, s, http.MethodGet,
		"/network/default/node/switch1/intent/projection?mode=topology")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	resp := decodeAPIResponse(t, w)
	if resp.Error != "" {
		t.Fatalf("error = %q, want empty", resp.Error)
	}
	raw, _ := json.Marshal(resp.Data)
	var proj map[string]map[string]map[string]string
	if err := json.Unmarshal(raw, &proj); err != nil {
		t.Fatalf("projection not RawConfigDB shape: %v", err)
	}
	if len(proj) == 0 {
		t.Fatal("projection empty after topology replay; expected setup-device entries")
	}
	if _, ok := proj["DEVICE_METADATA"]; !ok {
		t.Errorf("DEVICE_METADATA missing from projection; got tables: %v", mapKeys2(proj))
	}
}

// TestHandleConfigDBSnapshot_RouteRegistered — newtron#17 (Cluster D).
// Confirms the configdb snapshot route is registered and returns either a
// successful snapshot (200 + Data envelope) when a device is reachable on
// the host, or a clean connection-error envelope (500 + non-empty Error)
// when transport fails. Both outcomes prove the route is wired; full
// behavioral coverage of the live-device path lives in the actuated
// newtrun scenario `1node-vs-basic/05-configdb-snapshot-actuated`.
func TestHandleConfigDBSnapshot_RouteRegistered(t *testing.T) {
	s := newTestServer(t)

	w := httpDo(t, s, http.MethodGet, "/network/default/node/switch1/configdb")
	if w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed {
		t.Fatalf("route not registered: status = %d", w.Code)
	}
	resp := decodeAPIResponse(t, w)
	switch w.Code {
	case http.StatusOK:
		if resp.Data == nil {
			t.Error("status 200 but Data envelope missing")
		}
	case http.StatusInternalServerError:
		if resp.Error == "" {
			t.Error("status 500 but Error envelope empty")
		}
		if resp.Data != nil {
			t.Errorf("status 500 must not carry Data; got %v", resp.Data)
		}
	default:
		t.Errorf("unexpected status %d; want 200 (live device) or 500 (no transport); body: %s",
			w.Code, w.Body.String())
	}
}

// TestWriteResult_ChangesPopulated — newtron#11 (Cluster B). Exercises the
// real population path: LoadNetwork → BuildTopologyNode → Execute(CreateVLAN)
// → Commit → result.Changes carries the typed `sonic.ConfigChange` entries.
// No device connection required; topology mode runs setup-device so VLAN
// preconditions are satisfied.
func TestWriteResult_ChangesPopulated(t *testing.T) {
	specDir := filepath.Join(repoRoot(t), "newtrun", "topologies", "1node-vs", "specs")

	net, err := newtron.LoadNetwork(specDir)
	if err != nil {
		t.Fatalf("LoadNetwork: %v", err)
	}
	n, err := net.BuildTopologyNode("switch1")
	if err != nil {
		t.Fatalf("BuildTopologyNode: %v", err)
	}

	ctx := context.Background()
	result, err := n.Execute(ctx, newtron.ExecOpts{Execute: true, NoSave: true},
		func(ctx context.Context) error {
			return n.CreateVLAN(ctx, 100, newtron.VLANConfig{})
		})
	if err != nil {
		t.Fatalf("Execute(CreateVLAN): %v", err)
	}
	if len(result.Changes) == 0 {
		t.Fatal("WriteResult.Changes empty after CreateVLAN")
	}
	if len(result.Changes) != result.ChangeCount {
		t.Errorf("Changes count = %d, ChangeCount = %d; should match",
			len(result.Changes), result.ChangeCount)
	}
	found := false
	for _, c := range result.Changes {
		if c.Table == "VLAN" && c.Key == "Vlan100" && c.Type == sonic.ChangeTypeAdd {
			found = true
			break
		}
	}
	if !found {
		var summary []string
		for _, c := range result.Changes {
			summary = append(summary, c.Table+"|"+c.Key+":"+string(c.Type))
		}
		t.Errorf("expected VLAN|Vlan100 add in Changes; got: %v", summary)
	}
}

// TestWriteError_VerificationFailedEnvelope — newtron#21 (Cluster B envelope
// fix companion to #19). Confirms that writeError emits a 409 response whose
// body envelope carries the typed *WriteResult (Verification.Errors[] with
// DeviceResponse + PerWrite) as Data, per §46. Exercises the wire-format path
// directly without needing a live verify-failure.
func TestWriteError_VerificationFailedEnvelope(t *testing.T) {
	// Build a representative WriteResult that mirrors what a real verify-
	// failure path would produce: Verification.Errors with substrate fields
	// + PerWrite entries (verify_read kind, rejected result).
	wr := &newtron.WriteResult{
		Applied:     true,
		ChangeCount: 1,
		Verification: &newtron.VerificationResult{
			Passed: 0,
			Failed: 1,
			Errors: []newtron.VerificationError{{
				Table: "BGP_GLOBALS", Key: "default", Field: "local_asn",
				Expected: "65001", Actual: "99999",
				DeviceResponse: "local_asn=99999 router_id=10.0.0.1",
			}},
		},
		PerWrite: []sonic.PerSubstrateOp{{
			Seq: 0, Kind: sonic.PerWriteKindVerifyRead,
			Table: "BGP_GLOBALS", Key: "default",
			Result: sonic.PerWriteResultRejected,
			DeviceResponse: "local_asn=99999 router_id=10.0.0.1",
		}},
	}
	verErr := &newtron.VerificationFailedError{
		Device: "switch1", Passed: 0, Failed: 1, Total: 1, Result: wr,
	}

	w := httptest.NewRecorder()
	writeError(w, verErr)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (Conflict) for VerificationFailedError", w.Code)
	}

	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, w.Body.String())
	}
	if resp.Error == "" {
		t.Error("Error field empty; expected verify-failure message")
	}
	if resp.Data == nil {
		t.Fatal("Data field empty; envelope must carry the typed WriteResult per §46")
	}

	// The envelope's Data must round-trip back into a WriteResult with the
	// substrate intact — Verification.Errors[].DeviceResponse + PerWrite.
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal Data: %v", err)
	}
	var got newtron.WriteResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode WriteResult from Data: %v", err)
	}
	if got.Verification == nil || len(got.Verification.Errors) != 1 {
		t.Fatalf("Verification.Errors lost; got %+v", got.Verification)
	}
	if got.Verification.Errors[0].DeviceResponse != "local_asn=99999 router_id=10.0.0.1" {
		t.Errorf("DeviceResponse lost or mangled; got %q", got.Verification.Errors[0].DeviceResponse)
	}
	if len(got.PerWrite) != 1 || got.PerWrite[0].Kind != sonic.PerWriteKindVerifyRead {
		t.Errorf("PerWrite lost or mangled; got %+v", got.PerWrite)
	}
}

// TestProjectionDiff_HypotheticalCreateVLAN — newtron#4 (Phase 4). Loads the
// 1node-vs spec, builds switch1 in topology mode (setup-device baseline),
// then calls Node.ProjectionDiff with a hypothetical create-vlan op. Asserts
// the response contains the typed before/after RawConfigDB pair plus a diff
// where the new VLAN appears as an "extra" entry (present in after, absent
// from before — the add the op would produce). Confirms the snapshot/restore
// leaves the Node's observable state unchanged.
func TestProjectionDiff_HypotheticalCreateVLAN(t *testing.T) {
	specDir := filepath.Join(repoRoot(t), "newtrun", "topologies", "1node-vs", "specs")

	net, err := newtron.LoadNetwork(specDir)
	if err != nil {
		t.Fatalf("LoadNetwork: %v", err)
	}
	n, err := net.BuildTopologyNode("switch1")
	if err != nil {
		t.Fatalf("BuildTopologyNode: %v", err)
	}

	// Capture the projection before the hypothetical op for the restore check.
	preProj := n.Projection()
	hadVLAN100 := preProj["VLAN"] != nil && preProj["VLAN"]["Vlan100"] != nil
	if hadVLAN100 {
		t.Fatal("test setup: VLAN Vlan100 already present in projection")
	}

	ctx := context.Background()
	ops := []spec.TopologyStep{{
		URL:    "/create-vlan",
		Params: map[string]any{"vlan_id": 100},
	}}
	result, err := n.ProjectionDiff(ctx, ops)
	if err != nil {
		t.Fatalf("ProjectionDiff: %v", err)
	}

	if result.Before == nil {
		t.Error("Before is nil")
	}
	if result.After == nil {
		t.Error("After is nil")
	}
	if len(result.Diff) == 0 {
		t.Fatal("Diff empty — hypothetical create-vlan produced no delta")
	}

	// Before should have no VLAN|Vlan100.
	if result.Before["VLAN"] != nil && result.Before["VLAN"]["Vlan100"] != nil {
		t.Error("Before should not contain VLAN|Vlan100 (pre-op state)")
	}

	// After should have VLAN|Vlan100.
	if result.After["VLAN"] == nil || result.After["VLAN"]["Vlan100"] == nil {
		t.Errorf("After should contain VLAN|Vlan100 (post-op state); got tables: %v", mapKeys2(result.After))
	}

	// Diff should mark VLAN|Vlan100 as "extra" (added by the hypothetical op).
	var sawVLAN bool
	for _, e := range result.Diff {
		if e.Table == "VLAN" && e.Key == "Vlan100" {
			if e.Type != "extra" {
				t.Errorf("VLAN|Vlan100 diff Type = %q, want %q (added by op)", e.Type, "extra")
			}
			sawVLAN = true
		}
	}
	if !sawVLAN {
		var summary []string
		for _, e := range result.Diff {
			summary = append(summary, e.Table+"|"+e.Key+":"+e.Type)
		}
		t.Errorf("expected VLAN|Vlan100 in Diff; got: %v", summary)
	}

	// Node's observable projection must be restored.
	postProj := n.Projection()
	if postProj["VLAN"] != nil && postProj["VLAN"]["Vlan100"] != nil {
		t.Error("Projection not restored — VLAN|Vlan100 still present after ProjectionDiff")
	}
}

// mapKeys returns the keys of a map[string]any for error reporting.
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// mapKeys2 returns the keys of a nested RawConfigDB-shape map.
func mapKeys2(m map[string]map[string]map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// writeZoneFile writes zones/<name>.json = {} under dir, declaring an empty
// per-file zone. A fixture that places a node in a zone needs the zone to exist
// as a file (zones are per-file now, mirroring nodes); the node's zone
// reference validates against it at load.
func writeZoneFile(t *testing.T, dir, name string) {
	t.Helper()
	zonesDir := filepath.Join(dir, "zones")
	if err := os.MkdirAll(zonesDir, 0o755); err != nil {
		t.Fatalf("mkdir zones: %v", err)
	}
	if err := os.WriteFile(filepath.Join(zonesDir, name+".json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write zone %s: %v", name, err)
	}
}

// repoRoot walks up from this test file to the newtron repo root so tests
// can locate the newtrun topology network dirs without depending on cwd.
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
	specDir := filepath.Join(repoRoot(t), "networks", "1node-vs")
	s := NewServer(Config{})
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
	s.HTTPServer().Handler.ServeHTTP(w, req)
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
			"ListSpecInstances":       true,
			"ListServices":            true,
			"ShowService":             true,
			"ListIPVPNs":              true,
			"ShowIPVPN":               true,
			"ListMACVPNs":             true,
			"ShowMACVPN":              true,
			"ListQoSPolicies":         true,
			"ShowQoSPolicy":           true,
			"ListFilters":             true,
			"ShowFilter":              true,
			"ListPlatforms":           true,
			"ShowPlatform":            true,
			"ListRoutePolicies":       true,
			"ListPrefixLists":         true,
			"ShowPrefixList":          true,
			"ShowRoutePolicy":         true,
			"GetAllFeatures":          true,
			"GetFeatureDependencies":  true,
			"GetUnsupportedDueTo":     true,
			"PlatformSupportsFeature": true,
			// Spec writes
			"CreateService":         true,
			"DeleteService":         true,
			"CreateIPVPN":           true,
			"DeleteIPVPN":           true,
			"CreateMACVPN":          true,
			"DeleteMACVPN":          true,
			"CreateQoSPolicy":       true,
			"DeleteQoSPolicy":       true,
			"AddQoSQueue":           true,
			"UpdateQoSQueue":        true,
			"RemoveQoSQueue":        true,
			"CreateFilter":          true,
			"DeleteFilter":          true,
			"AddFilterRule":         true,
			"UpdateFilterRule":      true,
			"RemoveFilterRule":      true,
			"CreatePrefixList":      true,
			"DeletePrefixList":      true,
			"AddPrefixListEntry":    true,
			"RemovePrefixListEntry": true,
			"CreateRoutePolicy":     true,
			"DeleteRoutePolicy":     true,
			"AddRoutePolicyRule":    true,
			"UpdateRoutePolicyRule": true,
			"RemoveRoutePolicyRule": true,
			// NodeSpecs and Zones
			"ListNodeSpecs":  true,
			"ShowNodeSpec":   true,
			"CreateNodeSpec": true,
			"DeleteNodeSpec": true,
			"ListZones":      true,
			"ShowZone":       true,
			"CreateZone":     true,
			"DeleteZone":     true,
			// Spec Updates (#152) — full-replacement spec mutation
			"UpdateService":     true,
			"UpdateIPVPN":       true,
			"UpdateMACVPN":      true,
			"UpdateQoSPolicy":   true,
			"UpdateFilter":      true,
			"UpdatePrefixList":  true,
			"UpdateRoutePolicy": true,
			"UpdateNodeSpec":    true,
			"UpdateZone":        true,
			// Platform CRUD (#173)

			// Topology / Provision
			"HasTopology":          true,
			"GetTopology":          true, // #14: GET /networks/{netID}/topology (raw spec read; backs internal use + tests)
			"TopologyView":         true, // #14: GET /networks/{netID}/topology — served, provenance-enriched view
			"AddTopologyDevice":    true, // #15: POST /networks/{netID}/topology/create-node
			"DeleteTopologyDevice": true, // #15: DELETE /networks/{netID}/topology/nodes/{name}
			"UpdateTopologyDevice": true, // #15: PUT /networks/{netID}/topology/nodes/{name}
			"AddTopologyLink":      true, // #16: POST /networks/{netID}/topology/create-link
			"DeleteTopologyLink":   true, // #16: DELETE /networks/{netID}/topology/links/{device}/{interface}
			"TopologyNodeNames":    true,
			"IsHostDevice":         true,
			"GetHostConnection":    true,
			"InitDevice":           true,
			// Connection
			"ListNodes": true,
			// Device status (issue #75A+B)
			"ProbeOnline":   true, // GET /networks/{netID}/nodes/{device}/status
			"TopologyDrift": true, // GET /networks/{netID}/nodes/{device}/intent/topology-drift
			// Authorization-table inspector (issue #150)
			"GetAuthorization": true, // GET /networks/{netID}/authorization
			"AddSuperUser":     true, // POST /networks/{netID}/super-users
			"RemoveSuperUser":  true, // DELETE /networks/{netID}/super-users/{user}
			"SetSecret":        true, // POST /networks/{netID}/secrets
			"DeleteSecret":     true, // DELETE /networks/{netID}/secrets/{key}
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
			// DB queries
			"QueryConfigDB":       true,
			"ConfigDBTableKeys":   true,
			"ConfigDBEntryExists": true,
			"ConfigDBSnapshot":    true, // #17: GET /networks/{netID}/nodes/{device}/configdb
			"QueryStateDB":        true,
			// Write operations
			"AddBGPEVPNPeer":          true,
			"UpdateBGPEVPNPeer":       true,
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
			"UpdateStaticRoute":       true,
			"RemoveStaticRoute":       true,
			"CreateACL":               true,
			"DeleteACL":               true,
			"AddACLRule":              true,
			"UpdateACLRule":           true,
			"RemoveACLRule":           true,
			"CreatePortChannel":       true,
			"DeletePortChannel":       true,
			"AddPortChannelMember":    true,
			"RemovePortChannelMember": true,
			"ConfigReload":            true,
			"RestartService":          true,
			"RefreshBGP":              true, // POST /networks/{netID}/nodes/{device}/refresh-bgp
			"ExecCommand":             true,
			// Intent operations
			"Projection":     true, // #5: GET /networks/{netID}/nodes/{device}/intent/projection
			"ProjectionDiff": true, // #4: POST /networks/{netID}/nodes/{device}/intent/projection-diff
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
			"UpdateBGPPeer":        true,
			"RemoveBGPPeer":        true,
			"SetProperty":          true,
			"ClearProperty":        true,
			"ConfigureInterface":   true,
			"RemoveTrunkVLAN":      true,
			"UnconfigureInterface": true,
			"BindQoS":              true,
			"UnbindQoS":            true,
		},
	}

	// excludedMethods lists methods intentionally NOT exposed via HTTP.
	excludedMethods := map[string]map[string]string{
		"Network": {
			"EnableAuthorization":    "server-internal initialization — invoked by api.Server when --enforce-authorization is set (auth-design.md L3); not a request-handled action",
			"SetAuditLogger":         "server-internal initialization — api.Server hands each network its per-network audit logger on RegisterNetwork/ReloadNetwork (auth-design.md L1); not a request-handled action",
			"Authorize":              "server-internal permission gate — invoked by api.Server for the write-control reservation handlers (control.request/control.takeover); not itself a request-handled action",
			"BuildEmptyTopologyNode": "intent save/reload helpers — invoked via intent/save and intent/reload handlers",
			"BuildTopologyNode":      "intent save/reload helpers — invoked via intent/save and intent/reload handlers",
			"InitFromDeviceIntent":   "intent mode initialization — invoked by NodeActor.ensureActuatedIntent",
			"SaveDeviceIntents":      "intent save — invoked by handleSave via nodeActor.execute",
			"CheckAuthReadGate":      "auth gate helper invoked by handleGetAuthorization for the engage-when-configured PermAuthRead check (#187); not a request-handled action",
			"CheckAuditReadGate":     "auth gate helper invoked by handleAuditEvents and handleAuditIntegrity for the engage-when-configured PermAuditRead check (#196); not a request-handled action",
		},
		"Node": {
			"BindsService":        "internal helper for /service/{name}/projection — pre-check before ServiceProjection",
			"ServiceProjection":   "internal helper used by /service/{name}/projection handler per-actor; no separate per-Node endpoint",
			"SetDeviceMetadata":   "used internally by InitDevice; no direct HTTP endpoint",
			"Interface":           "interface access is via URL path, not a method call",
			"Lock":                "server handles locking internally via connectAndExecute",
			"Unlock":              "server handles locking internally via connectAndExecute",
			"Close":               "server handles connection lifecycle",
			"Commit":              "server handles commit via Execute/connectAndExecute",
			"PendingPreview":      "exposed through WriteResult.Preview in Execute",
			"PendingCount":        "exposed through WriteResult.ChangeCount",
			"Ping":                "server-internal connectivity check in NodeActor",
			"HasActuatedIntent":   "server-internal check for node initialization state",
			"HasUnsavedIntents":   "server-internal state tracking",
			"ClearUnsavedIntents": "server-internal state management",
			"DisconnectTransport": "server-internal lifecycle management",
			"RebuildProjection":   "called by execute() at start of each operation",
		},
		"Interface": {},
	}

	types := []struct {
		name string
		typ  reflect.Type
	}{
		{"Network", reflect.TypeOf((*newtron.Network)(nil))},
		{"Node", reflect.TypeOf((*newtron.Node)(nil))},
		{"Interface", reflect.TypeOf((*newtron.Interface)(nil))},
	}

	// authorizedMethods (auth-design.md L4) maps every HTTP-exposed
	// MUTATION method to the auth.Permission constant its checkPermission
	// call uses. Every method in coveredMethods must appear here OR in
	// readOnlyMethods below — the test refuses to compile a new mutation
	// surface without an explicit gate decision.
	authorizedMethods := map[string]map[string]auth.Permission{
		"Network": {
			"CreateService":         auth.PermSpecAuthor,
			"DeleteService":         auth.PermSpecAuthor,
			"CreateIPVPN":           auth.PermSpecAuthor,
			"DeleteIPVPN":           auth.PermSpecAuthor,
			"CreateMACVPN":          auth.PermSpecAuthor,
			"DeleteMACVPN":          auth.PermSpecAuthor,
			"CreateQoSPolicy":       auth.PermQoSCreate,
			"DeleteQoSPolicy":       auth.PermQoSDelete,
			"AddQoSQueue":           auth.PermSpecAuthor,
			"UpdateQoSQueue":        auth.PermSpecAuthor,
			"RemoveQoSQueue":        auth.PermSpecAuthor,
			"CreateFilter":          auth.PermFilterCreate,
			"DeleteFilter":          auth.PermFilterDelete,
			"AddFilterRule":         auth.PermSpecAuthor,
			"UpdateFilterRule":      auth.PermSpecAuthor,
			"RemoveFilterRule":      auth.PermSpecAuthor,
			"CreatePrefixList":      auth.PermSpecAuthor,
			"DeletePrefixList":      auth.PermSpecAuthor,
			"AddPrefixListEntry":    auth.PermSpecAuthor,
			"RemovePrefixListEntry": auth.PermSpecAuthor,
			"CreateRoutePolicy":     auth.PermSpecAuthor,
			"DeleteRoutePolicy":     auth.PermSpecAuthor,
			"AddRoutePolicyRule":    auth.PermSpecAuthor,
			"UpdateRoutePolicyRule": auth.PermSpecAuthor,
			"RemoveRoutePolicyRule": auth.PermSpecAuthor,
			"CreateNodeSpec":        auth.PermSpecAuthor,
			"DeleteNodeSpec":        auth.PermSpecAuthor,
			"CreateZone":            auth.PermSpecAuthor,
			"AddSuperUser":          auth.PermSpecAuthor, // meta-authz: spec.author scoped to super_users
			"RemoveSuperUser":       auth.PermSpecAuthor,
			"SetSecret":             auth.PermSpecAuthor, // spec.author scoped to secrets
			"DeleteSecret":          auth.PermSpecAuthor,
			"DeleteZone":            auth.PermSpecAuthor,
			"UpdateService":         auth.PermSpecAuthor,
			"UpdateIPVPN":           auth.PermSpecAuthor,
			"UpdateMACVPN":          auth.PermSpecAuthor,
			"UpdateQoSPolicy":       auth.PermSpecAuthor,
			"UpdateFilter":          auth.PermSpecAuthor,
			"UpdatePrefixList":      auth.PermSpecAuthor,
			"UpdateRoutePolicy":     auth.PermSpecAuthor,
			"UpdateNodeSpec":        auth.PermSpecAuthor,
			"UpdateZone":            auth.PermSpecAuthor,

			"AddTopologyDevice":    auth.PermSpecAuthor,
			"DeleteTopologyDevice": auth.PermSpecAuthor,
			"UpdateTopologyDevice": auth.PermSpecAuthor,
			"AddTopologyLink":      auth.PermSpecAuthor,
			"DeleteTopologyLink":   auth.PermSpecAuthor,
			"InitDevice":           auth.PermDeviceWrite,
		},
		"Node": {
			"AddBGPEVPNPeer":          auth.PermEVPNPeer,
			"UpdateBGPEVPNPeer":       auth.PermEVPNPeer,
			"RemoveBGPEVPNPeer":       auth.PermEVPNPeer,
			"BindMACVPN":              auth.PermEVPNMACVPN,
			"UnbindMACVPN":            auth.PermEVPNMACVPN,
			"SetupDevice":             auth.PermDeviceWrite,
			"CreateVLAN":              auth.PermVLANCreate,
			"DeleteVLAN":              auth.PermVLANDelete,
			"ConfigureIRB":            auth.PermVLANModify,
			"UnconfigureIRB":          auth.PermVLANModify,
			"CreateVRF":               auth.PermVRFCreate,
			"DeleteVRF":               auth.PermVRFDelete,
			"BindIPVPN":               auth.PermVRFBind,
			"UnbindIPVPN":             auth.PermVRFBind,
			"AddStaticRoute":          auth.PermVRFRoute,
			"UpdateStaticRoute":       auth.PermVRFRoute,
			"RemoveStaticRoute":       auth.PermVRFRoute,
			"CreateACL":               auth.PermACLCreate,
			"DeleteACL":               auth.PermACLDelete,
			"AddACLRule":              auth.PermACLModify,
			"UpdateACLRule":           auth.PermACLModify,
			"RemoveACLRule":           auth.PermACLModify,
			"CreatePortChannel":       auth.PermLAGCreate,
			"DeletePortChannel":       auth.PermLAGDelete,
			"AddPortChannelMember":    auth.PermLAGModify,
			"RemovePortChannelMember": auth.PermLAGModify,
			"ConfigReload":            auth.PermDeviceWrite,
			"RestartService":          auth.PermDeviceWrite,
			"RefreshBGP":              auth.PermDeviceWrite,
			"ExecCommand":             auth.PermDeviceWrite,
			"Save":                    auth.PermDeviceWrite,
			"Reconcile":               auth.PermDeviceWrite,
		},
		"Interface": {
			"ApplyService":         auth.PermServiceApply,
			"RemoveService":        auth.PermServiceRemove,
			"RefreshService":       auth.PermServiceApply,
			"BindACL":              auth.PermACLModify,
			"UnbindACL":            auth.PermACLModify,
			"AddBGPPeer":           auth.PermBGPPeer,
			"UpdateBGPPeer":        auth.PermBGPPeer,
			"RemoveBGPPeer":        auth.PermBGPPeer,
			"SetProperty":          auth.PermInterfaceModify,
			"ClearProperty":        auth.PermInterfaceModify,
			"ConfigureInterface":   auth.PermInterfaceModify,
			"RemoveTrunkVLAN":      auth.PermInterfaceModify,
			"UnconfigureInterface": auth.PermInterfaceModify,
			"BindQoS":              auth.PermQoSModify,
			"UnbindQoS":            auth.PermQoSModify,
		},
	}

	// readOnlyMethods (auth-design.md §3) names every HTTP-exposed
	// covered method that does NOT mutate state. Each value is a short
	// reason — usually "spec read", "device read", or a note about why
	// the operation is read-equivalent (Execute is the orchestration
	// wrapper; gates fire inside the fn it invokes).
	readOnlyMethods := map[string]map[string]string{
		"Network": {
			"ListSpecInstances":       "spec read",
			"ListServices":            "spec read",
			"ShowService":             "spec read",
			"ListIPVPNs":              "spec read",
			"ShowIPVPN":               "spec read",
			"ListMACVPNs":             "spec read",
			"ShowMACVPN":              "spec read",
			"ListQoSPolicies":         "spec read",
			"ShowQoSPolicy":           "spec read",
			"ListFilters":             "spec read",
			"ShowFilter":              "spec read",
			"ListPlatforms":           "spec read",
			"ShowPlatform":            "spec read",
			"ListRoutePolicies":       "spec read",
			"ListPrefixLists":         "spec read",
			"ShowPrefixList":          "spec read",
			"ShowRoutePolicy":         "spec read",
			"GetAllFeatures":          "spec read",
			"GetFeatureDependencies":  "spec read",
			"GetUnsupportedDueTo":     "spec read",
			"PlatformSupportsFeature": "spec read",
			"ListNodeSpecs":           "spec read",
			"ShowNodeSpec":            "spec read",
			"ListZones":               "spec read",
			"ShowZone":                "spec read",
			"HasTopology":             "spec read",
			"GetTopology":             "spec read",
			"TopologyView":            "spec read",
			"TopologyNodeNames":       "spec read",
			"IsHostDevice":            "spec read",
			"GetHostConnection":       "spec read",
			"ListNodes":               "spec read",
			"ProbeOnline":             "device read (TCP probe + newtlab port resolve)",
			"TopologyDrift":           "device read (diff topology against device CONFIG_DB)",
			"GetAuthorization":        "spec read (authorization-table inspector)",
		},
		"Node": {
			"DeviceInfo":              "device read",
			"ListInterfaceDetails":    "device read",
			"ShowInterfaceDetail":     "device read",
			"GetServiceBindingDetail": "device read",
			"VLANStatus":              "device read",
			"ShowVLAN":                "device read",
			"VRFStatus":               "device read",
			"ShowVRF":                 "device read",
			"ListACLs":                "device read",
			"ShowACL":                 "device read",
			"BGPStatus":               "device read",
			"EVPNStatus":              "device read",
			"LAGStatus":               "device read",
			"ShowLAGDetail":           "device read",
			"HealthCheck":             "device read",
			"CheckBGPSessions":        "device read",
			"GetRoute":                "device read",
			"GetRouteASIC":            "device read",
			"QueryConfigDB":           "device read",
			"ConfigDBTableKeys":       "device read",
			"ConfigDBEntryExists":     "device read",
			"ConfigDBSnapshot":        "device read",
			"QueryStateDB":            "device read",
			"Projection":              "intent read",
			"ProjectionDiff":          "intent dry-run preview (no device writes)",
			"Tree":                    "intent read",
			"Drift":                   "intent + device read",
			"Execute":                 "orchestration wrapper — gates fire on each mutation inside fn",
		},
		"Interface": {},
	}

	for _, tt := range types {
		covered := coveredMethods[tt.name]
		excluded := excludedMethods[tt.name]
		authorized := authorizedMethods[tt.name]
		readOnly := readOnlyMethods[tt.name]

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

			// L4 dimension: every covered method must be classified as
			// authorized (with a permission constant) OR read-only
			// (with a documented reason). Excluded methods (not HTTP-
			// exposed) skip this check — they have no caller to gate.
			if inCovered {
				_, inAuthorized := authorized[name]
				_, inReadOnly := readOnly[name]
				if !inAuthorized && !inReadOnly {
					t.Errorf("%s.%s: covered HTTP method missing L4 classification — add to authorizedMethods with a Permission or to readOnlyMethods with a reason (auth-design.md L4)", tt.name, name)
				}
				if inAuthorized && inReadOnly {
					t.Errorf("%s.%s: listed in both authorizedMethods and readOnlyMethods — remove from one", tt.name, name)
				}
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
		for name := range authorized {
			if !methodSet[name] {
				t.Errorf("%s.%s: listed in authorizedMethods but method does not exist", tt.name, name)
			}
			if !covered[name] {
				t.Errorf("%s.%s: listed in authorizedMethods but not in coveredMethods — only HTTP-exposed methods need authorization classification", tt.name, name)
			}
		}
		for name := range readOnly {
			if !methodSet[name] {
				t.Errorf("%s.%s: listed in readOnlyMethods but method does not exist", tt.name, name)
			}
			if !covered[name] {
				t.Errorf("%s.%s: listed in readOnlyMethods but not in coveredMethods", tt.name, name)
			}
		}
	}
}

// ============================================================================
// Phase 1 behavioral tests — exercise the new substrate-exposure endpoints
// against a topology-mode network (no device connection required).
// ============================================================================

// decodeAPIResponse decodes the body into httputil.APIResponse and fails on parse error.
func decodeAPIResponse(t *testing.T, w *httptest.ResponseRecorder) httputil.APIResponse {
	t.Helper()
	var resp httputil.APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode httputil.APIResponse: %v; body: %s", err, w.Body.String())
	}
	return resp
}

// TestHandleTopology_ReturnsSpecFile — newtron#14 (Cluster C). GET /topology
// returns the typed `spec.TopologySpecFile` with devices.switch1 present.
func TestHandleTopology_ReturnsSpecFile(t *testing.T) {
	s := newTestServer(t)

	w := httpDo(t, s, http.MethodGet, "/newtron/v1/networks/default/topology")
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
	devices, ok := topo["nodes"].(map[string]any)
	if !ok {
		t.Fatalf("topology.nodes not an object: %v", topo["nodes"])
	}
	if devices["switch1"] == nil {
		t.Errorf("topology.nodes.switch1 missing; got keys: %v", mapKeys(devices))
	}
}

// TestHandleTopology_CarriesSpecProvenance pins that GET /topology derives
// spec_kind/spec_name on its steps at serve time — for a whole network, in one
// call, with NO deployed lab (it's a spec read). 2node-vs-service's committed
// topology.json carries apply-service steps; the served step must report
// spec_kind=service / spec_name=transit, while primitive steps (setup-device)
// carry neither.
func TestHandleTopology_CarriesSpecProvenance(t *testing.T) {
	specDir := filepath.Join(repoRoot(t), "networks", "2node-vs-service")
	s := NewServer(Config{})
	if err := s.RegisterNetwork("svc", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	w := httpDo(t, s, http.MethodGet, "/newtron/v1/networks/svc/topology")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Devices map[string]struct {
				Steps []struct {
					URL      string `json:"url"`
					SpecKind string `json:"spec_kind"`
					SpecName string `json:"spec_name"`
				} `json:"steps"`
			} `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}

	var sawApplyService, sawPrimitive bool
	for dev, d := range env.Data.Devices {
		for _, step := range d.Steps {
			switch {
			case strings.HasSuffix(step.URL, "/apply-service"):
				sawApplyService = true
				if step.SpecKind != "service" || step.SpecName == "" {
					t.Errorf("%s %s: spec_kind=%q spec_name=%q, want service/<name>",
						dev, step.URL, step.SpecKind, step.SpecName)
				}
				// spec_name must be the spec's CANONICAL identity (so it equals
				// the GET /services key), not the raw step casing — the whole
				// point of the canonical-name bridge.
				if step.SpecName != util.NormalizeName(step.SpecName) {
					t.Errorf("%s %s: spec_name=%q is not canonical (want NormalizeName form)",
						dev, step.URL, step.SpecName)
				}
			case strings.HasSuffix(step.URL, "/setup-device"):
				sawPrimitive = true
				if step.SpecKind != "" || step.SpecName != "" {
					t.Errorf("%s %s: primitive carried spec_kind=%q spec_name=%q, want empty",
						dev, step.URL, step.SpecKind, step.SpecName)
				}
			}
		}
	}
	if !sawApplyService {
		t.Error("no apply-service step found — fixture changed; can't verify provenance")
	}
	if !sawPrimitive {
		t.Error("no setup-device step found — fixture changed; can't verify primitives stay unattributed")
	}
}

// TestHandleProjection_ReturnsRawConfigDB — newtron#5 (Cluster A). GET
// /intent/projection in topology mode returns the typed projection
// (`sonic.RawConfigDB`) built from intent replay. 1node-vs runs setup-device
// during topology load, so DEVICE_METADATA is the canonical sentinel entry.
func TestHandleProjection_ReturnsRawConfigDB(t *testing.T) {
	s := newTestServer(t)

	w := httpDo(t, s, http.MethodGet,
		"/newtron/v1/networks/default/nodes/switch1/intent/projection?mode=topology")
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

	w := httpDo(t, s, http.MethodGet, "/newtron/v1/networks/default/nodes/switch1/configdb")
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
	specDir := filepath.Join(repoRoot(t), "networks", "1node-vs")

	net, err := newtron.LoadNetwork(specDir, "", nil, nil, nil)
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
// DeviceResponse + DeviceOps) as Data, per §46. Exercises the wire-format path
// directly without needing a live verify-failure.
func TestWriteError_VerificationFailedEnvelope(t *testing.T) {
	// Build a representative WriteResult that mirrors what a real verify-
	// failure path would produce: Verification.Errors with substrate fields
	// + DeviceOps entries (verify_read kind, rejected result).
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
		DeviceOps: []sonic.DeviceOp{{
			Seq: 0, Kind: sonic.DeviceOpsKindVerifyRead,
			Table: "BGP_GLOBALS", Key: "default",
			Result:         sonic.DeviceOpsResultRejected,
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

	var resp httputil.APIResponse
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
	// substrate intact — Verification.Errors[].DeviceResponse + DeviceOps.
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
	if len(got.DeviceOps) != 1 || got.DeviceOps[0].Kind != sonic.DeviceOpsKindVerifyRead {
		t.Errorf("DeviceOps lost or mangled; got %+v", got.DeviceOps)
	}
}

// TestWriteError_ConflictEnvelope pins the §46 structured conflict payload: a
// ConflictError serializes its resource/name/references[]/force_available into
// the envelope Data (so clients branch on the shape, not a parsed message), and
// a spec-delete conflict neither advertises force in the message nor sets
// force_available — while a force-capable (nodeSpec) conflict does both.
func TestWriteError_ConflictEnvelope(t *testing.T) {
	specConflict := &newtron.ConflictError{
		Resource:   "IPVPNSpec",
		Name:       "IRB",
		References: []string{"ServiceSpec 'OVERLAY_IRB_A' (ipvpn)", "ServiceSpec 'OVERLAY_IRB_B' (ipvpn)"},
		// Force defaults false — specs have no force-cascade.
	}

	w := httptest.NewRecorder()
	writeError(w, specConflict)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	var resp httputil.APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, w.Body.String())
	}
	if resp.Data == nil {
		t.Fatal("Data empty; a conflict must carry its structured shape per §46")
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal Data: %v", err)
	}
	var got newtron.ConflictError
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode ConflictError from Data: %v", err)
	}
	if got.Resource != "IPVPNSpec" || got.Name != "IRB" || len(got.References) != 2 {
		t.Errorf("structured conflict lost: %+v", got)
	}
	if got.Force {
		t.Error("force_available should be false for a spec delete")
	}
	if strings.Contains(resp.Error, "force=true") {
		t.Errorf("spec-delete message wrongly advertises force: %q", resp.Error)
	}

	// A force-capable conflict (nodeSpec / topology-device) does advertise force.
	nodeSpecConflict := &newtron.ConflictError{
		Resource:   "nodeSpec",
		Name:       "leaf1",
		References: []string{"topology device 'leaf1'"},
		Force:      true,
	}
	if !strings.Contains(nodeSpecConflict.Error(), "force=true") {
		t.Errorf("force-capable message should advertise force: %q", nodeSpecConflict.Error())
	}
}

// TestDeleteService_ActiveBindingReturns409 pins the end-to-end wire contract
// the browser UI consumes: POST /delete-service for a service still applied on
// interfaces (apply-service topology steps) returns 409 with a structured
// ConflictError whose references enumerate every device:interface binding and
// whose force_available is true. This is the dimension the spec-reference guard
// cannot see — the service is referenced nowhere in the spec graph, only bound
// in topology steps.
func TestDeleteService_ActiveBindingReturns409(t *testing.T) {
	specDir := filepath.Join(repoRoot(t), "networks", "2node-vs-service")
	s := NewServer(Config{})
	if err := s.RegisterNetwork("svc", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	req := httptest.NewRequest(http.MethodPost,
		"/newtron/v1/networks/svc/delete-service",
		strings.NewReader(`{"name":"transit"}`))
	w := httptest.NewRecorder()
	s.HTTPServer().Handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
	var resp httputil.APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	raw, _ := json.Marshal(resp.Data)
	var got newtron.ConflictError
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode ConflictError from Data: %v", err)
	}
	if got.Resource != "ServiceSpec" || got.Name != "transit" {
		t.Errorf("conflict = %+v, want Resource=ServiceSpec Name=transit", got)
	}
	if !got.Force {
		t.Error("force_available should be true — bindings can be cascade-removed with ?force=true")
	}
	want := []string{"switch1:Ethernet0", "switch2:Ethernet0"}
	if !reflect.DeepEqual(got.References, want) {
		t.Errorf("references = %v, want %v", got.References, want)
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
	specDir := filepath.Join(repoRoot(t), "networks", "1node-vs")

	net, err := newtron.LoadNetwork(specDir, "", nil, nil, nil)
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

// copyTestSpecDir copies the 1node-vs network directory into t.TempDir() so
// topology CRUD tests can mutate freely without polluting the lab spec.
// Returns the temp spec path; cleanup is automatic via t.TempDir().
func copyTestSpecDir(t *testing.T) string {
	t.Helper()
	src := filepath.Join(repoRoot(t), "networks", "1node-vs")
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("readdir src: %v", err)
	}
	// Copy the per-file spec subdirs the loader reads (nodes/, zones/), one
	// level deep. Other subdirs (audit/ runtime output, suites/ scenarios) are
	// not spec inputs and are skipped.
	for _, sub := range []string{"nodes", "zones"} {
		files, err := os.ReadDir(filepath.Join(src, sub))
		if err != nil {
			continue // subdir may not exist for this network
		}
		if err := os.MkdirAll(filepath.Join(dst, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(src, sub, f.Name()))
			if err != nil {
				t.Fatalf("read %s/%s: %v", sub, f.Name(), err)
			}
			if err := os.WriteFile(filepath.Join(dst, sub, f.Name()), data, 0o644); err != nil {
				t.Fatalf("write %s/%s: %v", sub, f.Name(), err)
			}
		}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}
	return dst
}

// TestTopologyCRUD_AddDeleteDevice — newtron#15 (Phase 5). Round-trip a new
// topology device entry: write a nodeSpec + a TopologyNode spec, verify the
// add lands in topology.json, then delete and verify removal. Cleanup is
// implicit via t.TempDir().
func TestTopologyCRUD_AddDeleteDevice(t *testing.T) {
	specDir := copyTestSpecDir(t)
	// Add a node spec for switch2 (matches the 1:1 name convention).
	if err := os.WriteFile(
		filepath.Join(specDir, "nodes", "switch2.json"),
		[]byte(`{"mgmt_ip":"127.0.0.1","loopback_ip":"10.0.0.2","zone":"amer","platform":"sonic-vs","ssh_user":"admin","ssh_pass":"x","underlay_asn":65002}`),
		0o644,
	); err != nil {
		t.Fatalf("write nodeSpec: %v", err)
	}

	net, err := newtron.LoadNetwork(specDir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadNetwork: %v", err)
	}
	dev := &spec.TopologyNode{
		Ports: map[string]*spec.PortConfig{
			"Ethernet0": {AdminStatus: "up", MTU: 9100},
		},
	}
	if err := net.AddTopologyDevice(context.Background(), "switch2", dev); err != nil {
		t.Fatalf("AddTopologyDevice: %v", err)
	}
	topo := net.GetTopology()
	if topo.Nodes["switch2"] == nil {
		t.Fatal("switch2 missing from topology after Add")
	}

	if err := net.DeleteTopologyDevice(context.Background(), "switch2", false); err != nil {
		t.Fatalf("DeleteTopologyDevice: %v", err)
	}
	topo = net.GetTopology()
	if topo.Nodes["switch2"] != nil {
		t.Error("switch2 still in topology after Delete")
	}
}

// TestTopologyCRUD_DeleteDevice_RefusesWithReferringLink — Q1 (Option C)
// default behavior: refuse with *ConflictError when a link references the
// device, listing the referring link in References.
func TestTopologyCRUD_DeleteDevice_RefusesWithReferringLink(t *testing.T) {
	specDir := copyTestSpecDir(t)
	if err := os.WriteFile(
		filepath.Join(specDir, "nodes", "switch2.json"),
		[]byte(`{"mgmt_ip":"127.0.0.1","loopback_ip":"10.0.0.2","zone":"amer","platform":"sonic-vs","ssh_user":"admin","ssh_pass":"x","underlay_asn":65002}`),
		0o644,
	); err != nil {
		t.Fatalf("write nodeSpec: %v", err)
	}

	net, err := newtron.LoadNetwork(specDir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadNetwork: %v", err)
	}
	dev := &spec.TopologyNode{
		Ports: map[string]*spec.PortConfig{
			"Ethernet0": {AdminStatus: "up"},
		},
	}
	if err := net.AddTopologyDevice(context.Background(), "switch2", dev); err != nil {
		t.Fatalf("AddTopologyDevice: %v", err)
	}
	if err := net.AddTopologyLink(context.Background(), &spec.TopologyLink{
		A: "switch1:Ethernet0",
		Z: "switch2:Ethernet0",
	}); err != nil {
		t.Fatalf("AddTopologyLink: %v", err)
	}

	err = net.DeleteTopologyDevice(context.Background(), "switch2", false)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	var conflict *newtron.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *ConflictError, got %T: %v", err, err)
	}
	if len(conflict.References) == 0 {
		t.Error("ConflictError must list the referring link in References")
	}

	// force=true cascades.
	if err := net.DeleteTopologyDevice(context.Background(), "switch2", true); err != nil {
		t.Fatalf("force-delete: %v", err)
	}
	topo := net.GetTopology()
	if topo.Nodes["switch2"] != nil {
		t.Error("switch2 still in topology after force-delete")
	}
	if len(topo.Links) != 0 {
		t.Errorf("links not cascaded; got %d still wired", len(topo.Links))
	}
}

// TestTopologyCRUD_AddLink_RejectsAlreadyWired — a port participates in at
// most one link; AddTopologyLink refuses when an endpoint is already wired.
func TestTopologyCRUD_AddLink_RejectsAlreadyWired(t *testing.T) {
	specDir := copyTestSpecDir(t)
	if err := os.WriteFile(
		filepath.Join(specDir, "nodes", "switch2.json"),
		[]byte(`{"mgmt_ip":"127.0.0.1","loopback_ip":"10.0.0.2","zone":"amer","platform":"sonic-vs","ssh_user":"admin","ssh_pass":"x","underlay_asn":65002}`),
		0o644,
	); err != nil {
		t.Fatalf("write nodeSpec: %v", err)
	}

	net, err := newtron.LoadNetwork(specDir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadNetwork: %v", err)
	}
	if err := net.AddTopologyDevice(context.Background(), "switch2", &spec.TopologyNode{
		Ports: map[string]*spec.PortConfig{
			"Ethernet0": {AdminStatus: "up"},
			"Ethernet4": {AdminStatus: "up"},
		},
	}); err != nil {
		t.Fatalf("AddTopologyDevice: %v", err)
	}
	if err := net.AddTopologyLink(context.Background(), &spec.TopologyLink{
		A: "switch1:Ethernet0",
		Z: "switch2:Ethernet0",
	}); err != nil {
		t.Fatalf("AddTopologyLink: %v", err)
	}
	// Same endpoint reused — must refuse.
	err = net.AddTopologyLink(context.Background(), &spec.TopologyLink{
		A: "switch2:Ethernet0",
		Z: "switch1:Ethernet4",
	})
	if err == nil {
		t.Fatal("expected conflict on already-wired endpoint, got nil")
	}
	var conflict *newtron.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *ConflictError, got %T", err)
	}
}

// TestTopologyCRUD_DeleteLink_BySingleEndpoint — Q3 design: a port
// participates in at most one link, so passing one endpoint uniquely
// identifies the link to delete.
func TestTopologyCRUD_DeleteLink_BySingleEndpoint(t *testing.T) {
	specDir := copyTestSpecDir(t)
	if err := os.WriteFile(
		filepath.Join(specDir, "nodes", "switch2.json"),
		[]byte(`{"mgmt_ip":"127.0.0.1","loopback_ip":"10.0.0.2","zone":"amer","platform":"sonic-vs","ssh_user":"admin","ssh_pass":"x","underlay_asn":65002}`),
		0o644,
	); err != nil {
		t.Fatalf("write nodeSpec: %v", err)
	}
	net, err := newtron.LoadNetwork(specDir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadNetwork: %v", err)
	}
	if err := net.AddTopologyDevice(context.Background(), "switch2", &spec.TopologyNode{
		Ports: map[string]*spec.PortConfig{"Ethernet0": {AdminStatus: "up"}},
	}); err != nil {
		t.Fatalf("AddTopologyDevice: %v", err)
	}
	if err := net.AddTopologyLink(context.Background(), &spec.TopologyLink{
		A: "switch1:Ethernet0",
		Z: "switch2:Ethernet0",
	}); err != nil {
		t.Fatalf("AddTopologyLink: %v", err)
	}
	// Pass only the A endpoint; the link should be found and removed.
	if err := net.DeleteTopologyLink(context.Background(), "switch1:Ethernet0"); err != nil {
		t.Fatalf("DeleteTopologyLink by single endpoint: %v", err)
	}
	if len(net.GetTopology().Links) != 0 {
		t.Errorf("link not removed; %d remaining", len(net.GetTopology().Links))
	}

	// Same call now returns NotFoundError.
	err = net.DeleteTopologyLink(context.Background(), "switch1:Ethernet0")
	var nf *newtron.NotFoundError
	if !errors.As(err, &nf) {
		t.Errorf("expected *NotFoundError after second delete, got %T: %v", err, err)
	}
}

// TestTopologyCRUD_UpdateNode_Replace — Q2 default: full replacement of the
// TopologyNode entry under the given name.
func TestTopologyCRUD_UpdateNode_Replace(t *testing.T) {
	specDir := copyTestSpecDir(t)
	net, err := newtron.LoadNetwork(specDir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadNetwork: %v", err)
	}
	// Replace switch1 with a different Ports map (no steps for simplicity).
	replacement := &spec.TopologyNode{
		Ports: map[string]*spec.PortConfig{
			"Ethernet0":  {AdminStatus: "up", MTU: 1500},
			"Ethernet64": {AdminStatus: "down"},
		},
	}
	if err := net.UpdateTopologyDevice(context.Background(), "switch1", replacement); err != nil {
		t.Fatalf("UpdateTopologyDevice: %v", err)
	}
	topo := net.GetTopology()
	got := topo.Nodes["switch1"]
	if got.Ports["Ethernet0"].MTU != 1500 {
		t.Errorf("Update did not replace Ethernet0 fields; got %+v", got.Ports["Ethernet0"])
	}
	if len(got.Steps) != 0 {
		t.Error("Update should have replaced Steps with empty (full-replacement semantics)")
	}
}

// TestTopologyCRUD_DeleteNodeSpec_CascadeSymmetry — newtron#15 follow-on:
// DeleteNodeSpec refuses when a topology device shares the name; force=true
// cascades through DeleteTopologyDevice (which itself cascades to links).
func TestTopologyCRUD_DeleteNodeSpec_CascadeSymmetry(t *testing.T) {
	specDir := copyTestSpecDir(t)
	if err := os.WriteFile(
		filepath.Join(specDir, "nodes", "switch2.json"),
		[]byte(`{"mgmt_ip":"127.0.0.1","loopback_ip":"10.0.0.2","zone":"amer","platform":"sonic-vs","ssh_user":"admin","ssh_pass":"x","underlay_asn":65002}`),
		0o644,
	); err != nil {
		t.Fatalf("write nodeSpec: %v", err)
	}
	net, err := newtron.LoadNetwork(specDir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadNetwork: %v", err)
	}
	if err := net.AddTopologyDevice(context.Background(), "switch2", &spec.TopologyNode{
		Ports: map[string]*spec.PortConfig{"Ethernet0": {AdminStatus: "up"}},
	}); err != nil {
		t.Fatalf("AddTopologyDevice: %v", err)
	}

	// Refuses without force.
	err = net.DeleteNodeSpec(context.Background(), "switch2", newtron.ExecOpts{Execute: true}, false)
	if err == nil {
		t.Fatal("expected conflict on nodeSpec-delete-with-topology-device, got nil")
	}
	var conflict *newtron.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *ConflictError, got %T: %v", err, err)
	}

	// Force cascade.
	if err := net.DeleteNodeSpec(context.Background(), "switch2", newtron.ExecOpts{Execute: true}, true); err != nil {
		t.Fatalf("force-delete nodeSpec: %v", err)
	}
	topo := net.GetTopology()
	if topo.Nodes["switch2"] != nil {
		t.Error("switch2 topology device still present after force-delete-node cascade")
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

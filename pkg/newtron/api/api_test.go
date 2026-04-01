package api

import (
	"reflect"
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron"
)

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
			"Tree":      true,
			"Drift":     true,
			"Reconcile": true,
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

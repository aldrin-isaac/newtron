package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron"
)

// buildMux creates the HTTP mux with all routes registered.
func (s *Server) buildMux() http.Handler {
	mux := http.NewServeMux()

	// ====================================================================
	// Server management
	// ====================================================================
	mux.HandleFunc("POST /network", s.handleRegisterNetwork)
	mux.HandleFunc("GET /network", s.handleListNetworks)
	mux.HandleFunc("DELETE /network/{netID}", s.handleUnregisterNetwork)
	mux.HandleFunc("POST /network/{netID}/reload", s.handleReloadNetwork)

	// ====================================================================
	// Network spec reads
	// ====================================================================
	mux.HandleFunc("GET /network/{netID}/service", s.handleListServices)
	mux.HandleFunc("GET /network/{netID}/service/{name}", s.handleShowService)
	mux.HandleFunc("GET /network/{netID}/ipvpn", s.handleListIPVPNs)
	mux.HandleFunc("GET /network/{netID}/ipvpn/{name}", s.handleShowIPVPN)
	mux.HandleFunc("GET /network/{netID}/macvpn", s.handleListMACVPNs)
	mux.HandleFunc("GET /network/{netID}/macvpn/{name}", s.handleShowMACVPN)
	mux.HandleFunc("GET /network/{netID}/qos-policy", s.handleListQoSPolicies)
	mux.HandleFunc("GET /network/{netID}/qos-policy/{name}", s.handleShowQoSPolicy)
	mux.HandleFunc("GET /network/{netID}/filter", s.handleListFilters)
	mux.HandleFunc("GET /network/{netID}/filter/{name}", s.handleShowFilter)
	mux.HandleFunc("GET /network/{netID}/platform", s.handleListPlatforms)
	mux.HandleFunc("GET /network/{netID}/platform/{name}", s.handleShowPlatform)
	mux.HandleFunc("GET /network/{netID}/route-policy", s.handleListRoutePolicies)
	mux.HandleFunc("GET /network/{netID}/route-policy/{name}", s.handleShowRoutePolicy)
	mux.HandleFunc("GET /network/{netID}/prefix-list", s.handleListPrefixLists)
	mux.HandleFunc("GET /network/{netID}/prefix-list/{name}", s.handleShowPrefixList)
	mux.HandleFunc("GET /network/{netID}/topology/node", s.handleTopologyDeviceNames)
	mux.HandleFunc("GET /network/{netID}/host/{name}", s.handleGetHostProfile)
	mux.HandleFunc("GET /network/{netID}/feature", s.handleGetAllFeatures)
	mux.HandleFunc("GET /network/{netID}/feature/{name}/dependency", s.handleGetFeatureDependencies)
	mux.HandleFunc("GET /network/{netID}/feature/{name}/unsupported-due-to", s.handleGetUnsupportedDueTo)
	mux.HandleFunc("GET /network/{netID}/platform/{name}/supports/{feature}", s.handlePlatformSupportsFeature)
	mux.HandleFunc("GET /network/{netID}/profile", s.handleListProfiles)
	mux.HandleFunc("GET /network/{netID}/profile/{name}", s.handleShowProfile)
	mux.HandleFunc("GET /network/{netID}/zone", s.handleListZones)
	mux.HandleFunc("GET /network/{netID}/zone/{name}", s.handleShowZone)

	// ====================================================================
	// Network spec writes
	// ====================================================================
	mux.HandleFunc("POST /network/{netID}/service", s.handleCreateService)
	mux.HandleFunc("DELETE /network/{netID}/service/{name}", s.handleDeleteService)
	mux.HandleFunc("POST /network/{netID}/ipvpn", s.handleCreateIPVPN)
	mux.HandleFunc("DELETE /network/{netID}/ipvpn/{name}", s.handleDeleteIPVPN)
	mux.HandleFunc("POST /network/{netID}/macvpn", s.handleCreateMACVPN)
	mux.HandleFunc("DELETE /network/{netID}/macvpn/{name}", s.handleDeleteMACVPN)
	mux.HandleFunc("POST /network/{netID}/qos-policy", s.handleCreateQoSPolicy)
	mux.HandleFunc("DELETE /network/{netID}/qos-policy/{name}", s.handleDeleteQoSPolicy)
	mux.HandleFunc("POST /network/{netID}/qos-policy/{name}/queue", s.handleAddQoSQueue)
	mux.HandleFunc("DELETE /network/{netID}/qos-policy/{name}/queue/{id}", s.handleRemoveQoSQueue)
	mux.HandleFunc("POST /network/{netID}/filter", s.handleCreateFilter)
	mux.HandleFunc("DELETE /network/{netID}/filter/{name}", s.handleDeleteFilter)
	mux.HandleFunc("POST /network/{netID}/filter/{name}/rule", s.handleAddFilterRule)
	mux.HandleFunc("DELETE /network/{netID}/filter/{name}/rule/{seq}", s.handleRemoveFilterRule)
	mux.HandleFunc("POST /network/{netID}/prefix-list", s.handleCreatePrefixList)
	mux.HandleFunc("DELETE /network/{netID}/prefix-list/{name}", s.handleDeletePrefixList)
	mux.HandleFunc("POST /network/{netID}/prefix-list/{name}/entry", s.handleAddPrefixListEntry)
	mux.HandleFunc("DELETE /network/{netID}/prefix-list/{name}/entry/{prefix...}", s.handleRemovePrefixListEntry)
	mux.HandleFunc("POST /network/{netID}/route-policy", s.handleCreateRoutePolicy)
	mux.HandleFunc("DELETE /network/{netID}/route-policy/{name}", s.handleDeleteRoutePolicy)
	mux.HandleFunc("POST /network/{netID}/route-policy/{name}/rule", s.handleAddRoutePolicyRule)
	mux.HandleFunc("DELETE /network/{netID}/route-policy/{name}/rule/{seq}", s.handleRemoveRoutePolicyRule)
	mux.HandleFunc("POST /network/{netID}/profile", s.handleCreateProfile)
	mux.HandleFunc("DELETE /network/{netID}/profile/{name}", s.handleDeleteProfile)
	mux.HandleFunc("POST /network/{netID}/zone", s.handleCreateZone)
	mux.HandleFunc("DELETE /network/{netID}/zone/{name}", s.handleDeleteZone)

	// ====================================================================
	// Network provision
	// ====================================================================
	mux.HandleFunc("POST /network/{netID}/provision", s.handleProvisionDevices)
	mux.HandleFunc("POST /network/{netID}/composite/{device}", s.handleGenerateDeviceComposite)
	mux.HandleFunc("POST /network/{netID}/init/{device}", s.handleInitDevice)

	// ====================================================================
	// Node read operations
	// ====================================================================
	mux.HandleFunc("GET /network/{netID}/node/{device}/info", s.handleNodeInfo)
	mux.HandleFunc("GET /network/{netID}/node/{device}/interface", s.handleListInterfaces)
	mux.HandleFunc("GET /network/{netID}/node/{device}/interface/{name}", s.handleShowInterface)
	mux.HandleFunc("GET /network/{netID}/node/{device}/interface/{name}/binding", s.handleShowServiceBinding)
	mux.HandleFunc("GET /network/{netID}/node/{device}/vlan", s.handleListVLANs)
	mux.HandleFunc("GET /network/{netID}/node/{device}/vlan/{id}", s.handleShowVLAN)
	mux.HandleFunc("GET /network/{netID}/node/{device}/vrf", s.handleListVRFs)
	mux.HandleFunc("GET /network/{netID}/node/{device}/vrf/{name}", s.handleShowVRF)
	mux.HandleFunc("GET /network/{netID}/node/{device}/acl", s.handleListACLs)
	mux.HandleFunc("GET /network/{netID}/node/{device}/acl/{name}", s.handleShowACL)
	mux.HandleFunc("GET /network/{netID}/node/{device}/bgp/status", s.handleBGPStatus)
	mux.HandleFunc("GET /network/{netID}/node/{device}/evpn/status", s.handleEVPNStatus)
	mux.HandleFunc("GET /network/{netID}/node/{device}/health", s.handleHealthCheck)
	mux.HandleFunc("GET /network/{netID}/node/{device}/lag", s.handleListLAGs)
	mux.HandleFunc("GET /network/{netID}/node/{device}/neighbor", s.handleListNeighbors)
	mux.HandleFunc("GET /network/{netID}/node/{device}/route/{vrf}/{prefix...}", s.handleGetRoute)
	mux.HandleFunc("GET /network/{netID}/node/{device}/route-asic/{prefix...}", s.handleGetRouteASIC)

	// ====================================================================
	// Node write operations
	// ====================================================================
	mux.HandleFunc("POST /network/{netID}/node/{device}/execute", s.handleExecute)
	mux.HandleFunc("POST /network/{netID}/node/{device}/configure-bgp", s.handleConfigureBGP)
	mux.HandleFunc("POST /network/{netID}/node/{device}/setup-evpn", s.handleSetupEVPN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/teardown-evpn", s.handleTeardownEVPN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/configure-loopback", s.handleConfigureLoopback)
	mux.HandleFunc("POST /network/{netID}/node/{device}/remove-loopback", s.handleRemoveLoopback)
	mux.HandleFunc("POST /network/{netID}/node/{device}/config-reload", s.handleConfigReload)
	mux.HandleFunc("POST /network/{netID}/node/{device}/save-config", s.handleSaveConfig)
	mux.HandleFunc("POST /network/{netID}/node/{device}/cleanup", s.handleCleanup)
	mux.HandleFunc("POST /network/{netID}/node/{device}/ssh-command", s.handleSSHCommand)
	mux.HandleFunc("POST /network/{netID}/node/{device}/vlan", s.handleCreateVLAN)
	mux.HandleFunc("DELETE /network/{netID}/node/{device}/vlan/{id}", s.handleDeleteVLAN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/vlan/{id}/member", s.handleAddVLANMember)
	mux.HandleFunc("DELETE /network/{netID}/node/{device}/vlan/{id}/member/{iface}", s.handleRemoveVLANMember)
	mux.HandleFunc("POST /network/{netID}/node/{device}/svi", s.handleConfigureSVI)
	mux.HandleFunc("POST /network/{netID}/node/{device}/remove-svi", s.handleRemoveSVI)
	mux.HandleFunc("POST /network/{netID}/node/{device}/vrf", s.handleCreateVRF)
	mux.HandleFunc("DELETE /network/{netID}/node/{device}/vrf/{name}", s.handleDeleteVRF)
	mux.HandleFunc("POST /network/{netID}/node/{device}/vrf/{name}/interface", s.handleAddVRFInterface)
	mux.HandleFunc("DELETE /network/{netID}/node/{device}/vrf/{name}/interface/{iface}", s.handleRemoveVRFInterface)
	mux.HandleFunc("POST /network/{netID}/node/{device}/vrf/{name}/bind-ipvpn", s.handleBindIPVPN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/vrf/{name}/unbind-ipvpn", s.handleUnbindIPVPN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/vrf/{name}/route", s.handleAddStaticRoute)
	mux.HandleFunc("DELETE /network/{netID}/node/{device}/vrf/{name}/route/{prefix...}", s.handleRemoveStaticRoute)
	mux.HandleFunc("POST /network/{netID}/node/{device}/acl", s.handleCreateACL)
	mux.HandleFunc("DELETE /network/{netID}/node/{device}/acl/{name}", s.handleDeleteACL)
	mux.HandleFunc("POST /network/{netID}/node/{device}/acl/{name}/rule", s.handleAddACLRule)
	mux.HandleFunc("DELETE /network/{netID}/node/{device}/acl/{name}/rule/{rule}", s.handleRemoveACLRule)
	mux.HandleFunc("POST /network/{netID}/node/{device}/portchannel", s.handleCreatePortChannel)
	mux.HandleFunc("DELETE /network/{netID}/node/{device}/portchannel/{name}", s.handleDeletePortChannel)
	mux.HandleFunc("POST /network/{netID}/node/{device}/portchannel/{name}/member", s.handleAddPortChannelMember)
	mux.HandleFunc("DELETE /network/{netID}/node/{device}/portchannel/{name}/member/{iface}", s.handleRemovePortChannelMember)
	mux.HandleFunc("POST /network/{netID}/node/{device}/add-bgp-neighbor", s.handleAddBGPNeighborNode)
	mux.HandleFunc("POST /network/{netID}/node/{device}/remove-bgp-neighbor", s.handleRemoveBGPNeighborNode)
	mux.HandleFunc("POST /network/{netID}/node/{device}/remove-bgp-globals", s.handleRemoveBGPGlobals)
	mux.HandleFunc("POST /network/{netID}/node/{device}/apply-frr-defaults", s.handleApplyFRRDefaults)
	mux.HandleFunc("POST /network/{netID}/node/{device}/restart-service", s.handleRestartService)
	mux.HandleFunc("POST /network/{netID}/node/{device}/set-metadata", s.handleSetDeviceMetadata)
	mux.HandleFunc("POST /network/{netID}/node/{device}/refresh", s.handleRefresh)
	mux.HandleFunc("POST /network/{netID}/node/{device}/apply-qos", s.handleNodeApplyQoS)
	mux.HandleFunc("POST /network/{netID}/node/{device}/remove-qos", s.handleNodeRemoveQoS)
	mux.HandleFunc("POST /network/{netID}/node/{device}/verify-committed", s.handleVerifyCommitted)
	mux.HandleFunc("GET /network/{netID}/node/{device}/configdb/{table}", s.handleConfigDBTableKeys)
	mux.HandleFunc("GET /network/{netID}/node/{device}/configdb/{table}/{key}", s.handleQueryConfigDB)
	mux.HandleFunc("GET /network/{netID}/node/{device}/configdb/{table}/{key}/exists", s.handleConfigDBEntryExists)
	mux.HandleFunc("GET /network/{netID}/node/{device}/statedb/{table}/{key}", s.handleQueryStateDB)
	mux.HandleFunc("GET /network/{netID}/node/{device}/bgp/check", s.handleCheckBGPSessions)
	mux.HandleFunc("GET /network/{netID}/node/{device}/lag/{name}", s.handleShowLAGDetail)
	// Intent operations
	mux.HandleFunc("GET /network/{netID}/node/{device}/intents", s.handleListIntents)
	// Singular: only one zombie can exist per device at a time.
	mux.HandleFunc("GET /network/{netID}/node/{device}/intents/zombie", s.handleReadZombieNew)
	mux.HandleFunc("POST /network/{netID}/node/{device}/intents/zombie/rollback", s.handleRollbackZombieNew)
	mux.HandleFunc("POST /network/{netID}/node/{device}/intents/zombie/clear", s.handleClearZombieNew)

	// History
	mux.HandleFunc("GET /network/{netID}/node/{device}/history", s.handleReadHistory)
	mux.HandleFunc("POST /network/{netID}/node/{device}/history/rollback", s.handleRollbackHistory)

	// Device settings
	mux.HandleFunc("GET /network/{netID}/node/{device}/settings", s.handleReadSettings)
	mux.HandleFunc("PUT /network/{netID}/node/{device}/settings", s.handleWriteSettings)

	// Drift detection
	mux.HandleFunc("GET /network/{netID}/node/{device}/drift", s.handleDetectDrift)
	mux.HandleFunc("GET /network/{netID}/drift", s.handleNetworkDrift)

	// ====================================================================
	// Node composite operations
	// ====================================================================
	mux.HandleFunc("POST /network/{netID}/node/{device}/composite/generate", s.handleCompositeGenerate)
	mux.HandleFunc("POST /network/{netID}/node/{device}/composite/verify", s.handleCompositeVerify)
	mux.HandleFunc("POST /network/{netID}/node/{device}/composite/deliver", s.handleCompositeDeliver)

	// ====================================================================
	// Interface operations
	// ====================================================================
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/apply-service", s.handleApplyService)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/remove-service", s.handleRemoveService)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/refresh-service", s.handleRefreshService)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/set-ip", s.handleSetIP)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/remove-ip", s.handleRemoveIP)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/set-vrf", s.handleSetVRF)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/bind-acl", s.handleBindACL)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/unbind-acl", s.handleUnbindACL)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/bind-macvpn", s.handleBindMACVPN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/unbind-macvpn", s.handleUnbindMACVPN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/add-bgp-neighbor", s.handleAddBGPNeighbor)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/remove-bgp-neighbor", s.handleRemoveBGPNeighbor)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/set", s.handleInterfaceSet)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/apply-qos", s.handleApplyInterfaceQoS)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/remove-qos", s.handleRemoveInterfaceQoS)

	// Apply middleware chain: recovery → logger → requestID → timeout → mux
	var handler http.Handler = mux
	handler = withTimeout(5 * time.Minute)(handler)
	handler = withRequestID(handler)
	handler = withLogger(s.logger)(handler)
	handler = withRecovery(s.logger)(handler)

	return handler
}

// ============================================================================
// JSON helpers
// ============================================================================

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIResponse{Data: data})
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, err error) {
	status := httpStatusFromError(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIResponse{Error: err.Error()})
}

// decodeJSON decodes a JSON request body into v.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(v)
}

// requireNetwork looks up the NetworkActor or writes a 404.
func (s *Server) requireNetwork(w http.ResponseWriter, r *http.Request) *NetworkActor {
	netID := r.PathValue("netID")
	na := s.getNetwork(netID)
	if na == nil {
		writeError(w, &notRegisteredError{netID})
		return nil
	}
	return na
}

// requireNodeActor looks up the NetworkActor and NodeActor, or writes an error.
func (s *Server) requireNodeActor(w http.ResponseWriter, r *http.Request) (*NetworkActor, *NodeActor) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return nil, nil
	}
	device := r.PathValue("device")
	return na, na.getNodeActor(device)
}

// execOpts reads dry_run and no_save query params.
func execOpts(r *http.Request) newtron.ExecOpts {
	dryRun := r.URL.Query().Get("dry_run") == "true"
	noSave := r.URL.Query().Get("no_save") == "true"
	return newtron.ExecOpts{
		Execute: !dryRun,
		NoSave:  noSave,
	}
}

// pathInt parses an integer from a URL path parameter.
func pathInt(r *http.Request, name string) (int, error) {
	return strconv.Atoi(r.PathValue(name))
}

// interfaceName normalizes interface names from URL paths.
// Ethernet0 stays as-is; Ethernet0%2F1 becomes Ethernet0/1.
func interfaceName(r *http.Request) string {
	name := r.PathValue("name")
	return strings.ReplaceAll(name, "%2F", "/")
}


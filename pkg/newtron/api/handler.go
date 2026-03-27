package api

import (
	"encoding/json"
	"io"
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
	mux.HandleFunc("POST /network/{netID}/unregister", s.handleUnregisterNetwork)
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
	// Network spec writes (RPC-style: verb in URL, POST for all writes)
	// ====================================================================
	mux.HandleFunc("POST /network/{netID}/create-service", s.handleCreateService)
	mux.HandleFunc("POST /network/{netID}/delete-service", s.handleDeleteService)
	mux.HandleFunc("POST /network/{netID}/create-ipvpn", s.handleCreateIPVPN)
	mux.HandleFunc("POST /network/{netID}/delete-ipvpn", s.handleDeleteIPVPN)
	mux.HandleFunc("POST /network/{netID}/create-macvpn", s.handleCreateMACVPN)
	mux.HandleFunc("POST /network/{netID}/delete-macvpn", s.handleDeleteMACVPN)
	mux.HandleFunc("POST /network/{netID}/create-qos-policy", s.handleCreateQoSPolicy)
	mux.HandleFunc("POST /network/{netID}/delete-qos-policy", s.handleDeleteQoSPolicy)
	mux.HandleFunc("POST /network/{netID}/add-qos-queue", s.handleAddQoSQueue)
	mux.HandleFunc("POST /network/{netID}/remove-qos-queue", s.handleRemoveQoSQueue)
	mux.HandleFunc("POST /network/{netID}/create-filter", s.handleCreateFilter)
	mux.HandleFunc("POST /network/{netID}/delete-filter", s.handleDeleteFilter)
	mux.HandleFunc("POST /network/{netID}/add-filter-rule", s.handleAddFilterRule)
	mux.HandleFunc("POST /network/{netID}/remove-filter-rule", s.handleRemoveFilterRule)
	mux.HandleFunc("POST /network/{netID}/create-prefix-list", s.handleCreatePrefixList)
	mux.HandleFunc("POST /network/{netID}/delete-prefix-list", s.handleDeletePrefixList)
	mux.HandleFunc("POST /network/{netID}/add-prefix-list-entry", s.handleAddPrefixListEntry)
	mux.HandleFunc("POST /network/{netID}/remove-prefix-list-entry", s.handleRemovePrefixListEntry)
	mux.HandleFunc("POST /network/{netID}/create-route-policy", s.handleCreateRoutePolicy)
	mux.HandleFunc("POST /network/{netID}/delete-route-policy", s.handleDeleteRoutePolicy)
	mux.HandleFunc("POST /network/{netID}/add-route-policy-rule", s.handleAddRoutePolicyRule)
	mux.HandleFunc("POST /network/{netID}/remove-route-policy-rule", s.handleRemoveRoutePolicyRule)
	mux.HandleFunc("POST /network/{netID}/create-profile", s.handleCreateProfile)
	mux.HandleFunc("POST /network/{netID}/delete-profile", s.handleDeleteProfile)
	mux.HandleFunc("POST /network/{netID}/create-zone", s.handleCreateZone)
	mux.HandleFunc("POST /network/{netID}/delete-zone", s.handleDeleteZone)

	// ====================================================================
	// Network provision
	// ====================================================================
	mux.HandleFunc("POST /network/{netID}/provision", s.handleProvisionDevices)
	mux.HandleFunc("POST /network/{netID}/node/{device}/init-device", s.handleInitDevice)

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
	mux.HandleFunc("GET /network/{netID}/node/{device}/intent/tree", s.handleIntentTree)
	mux.HandleFunc("GET /network/{netID}/node/{device}/lag", s.handleListLAGs)
	mux.HandleFunc("GET /network/{netID}/node/{device}/neighbor", s.handleListNeighbors)
	mux.HandleFunc("GET /network/{netID}/node/{device}/route/{vrf}/{prefix...}", s.handleGetRoute)
	mux.HandleFunc("GET /network/{netID}/node/{device}/route-asic/{prefix...}", s.handleGetRouteASIC)

	// ====================================================================
	// Node write operations (RPC-style: verb in URL, POST for all writes)
	// ====================================================================
	mux.HandleFunc("POST /network/{netID}/node/{device}/bind-macvpn", s.handleNodeBindMACVPN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/unbind-macvpn", s.handleNodeUnbindMACVPN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/reload-config", s.handleReloadConfig)
	mux.HandleFunc("POST /network/{netID}/node/{device}/save-config", s.handleSaveConfig)
	mux.HandleFunc("POST /network/{netID}/node/{device}/ssh-command", s.handleSSHCommand)
	mux.HandleFunc("POST /network/{netID}/node/{device}/create-vlan", s.handleCreateVLAN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/delete-vlan", s.handleDeleteVLAN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/configure-irb", s.handleConfigureIRB)
	mux.HandleFunc("POST /network/{netID}/node/{device}/unconfigure-irb", s.handleUnconfigureIRB)
	mux.HandleFunc("POST /network/{netID}/node/{device}/create-vrf", s.handleCreateVRF)
	mux.HandleFunc("POST /network/{netID}/node/{device}/delete-vrf", s.handleDeleteVRF)
	mux.HandleFunc("POST /network/{netID}/node/{device}/bind-ipvpn", s.handleBindIPVPN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/unbind-ipvpn", s.handleUnbindIPVPN)
	mux.HandleFunc("POST /network/{netID}/node/{device}/add-static-route", s.handleAddStaticRoute)
	mux.HandleFunc("POST /network/{netID}/node/{device}/remove-static-route", s.handleRemoveStaticRoute)
	mux.HandleFunc("POST /network/{netID}/node/{device}/create-acl", s.handleCreateACL)
	mux.HandleFunc("POST /network/{netID}/node/{device}/delete-acl", s.handleDeleteACL)
	mux.HandleFunc("POST /network/{netID}/node/{device}/add-acl-rule", s.handleAddACLRule)
	mux.HandleFunc("POST /network/{netID}/node/{device}/remove-acl-rule", s.handleRemoveACLRule)
	mux.HandleFunc("POST /network/{netID}/node/{device}/create-portchannel", s.handleCreatePortChannel)
	mux.HandleFunc("POST /network/{netID}/node/{device}/delete-portchannel", s.handleDeletePortChannel)
	mux.HandleFunc("POST /network/{netID}/node/{device}/add-portchannel-member", s.handleAddPortChannelMember)
	mux.HandleFunc("POST /network/{netID}/node/{device}/remove-portchannel-member", s.handleRemovePortChannelMember)
	mux.HandleFunc("POST /network/{netID}/node/{device}/add-bgp-evpn-peer", s.handleAddBGPEVPNPeer)
	mux.HandleFunc("POST /network/{netID}/node/{device}/remove-bgp-evpn-peer", s.handleRemoveBGPEVPNPeer)
	mux.HandleFunc("POST /network/{netID}/node/{device}/restart-daemon", s.handleRestartDaemon)
	mux.HandleFunc("POST /network/{netID}/node/{device}/setup-device", s.handleSetupDevice)
	mux.HandleFunc("POST /network/{netID}/node/{device}/refresh", s.handleRefresh)
	mux.HandleFunc("POST /network/{netID}/node/{device}/verify-committed", s.handleVerifyCommitted)
	mux.HandleFunc("GET /network/{netID}/node/{device}/configdb/{table}", s.handleConfigDBTableKeys)
	mux.HandleFunc("GET /network/{netID}/node/{device}/configdb/{table}/{key}", s.handleQueryConfigDB)
	mux.HandleFunc("GET /network/{netID}/node/{device}/configdb/{table}/{key}/exists", s.handleConfigDBEntryExists)
	mux.HandleFunc("GET /network/{netID}/node/{device}/statedb/{table}/{key}", s.handleQueryStateDB)
	mux.HandleFunc("GET /network/{netID}/node/{device}/bgp/check", s.handleCheckBGPSessions)
	mux.HandleFunc("GET /network/{netID}/node/{device}/lag/{name}", s.handleShowLAGDetail)
	// Intent operations
	mux.HandleFunc("GET /network/{netID}/node/{device}/intents", s.handleListIntents)
	mux.HandleFunc("GET /network/{netID}/node/{device}/zombie", s.handleReadZombie)
	mux.HandleFunc("POST /network/{netID}/node/{device}/rollback-zombie", s.handleRollbackZombie)
	mux.HandleFunc("POST /network/{netID}/node/{device}/clear-zombie", s.handleClearZombie)

	// History
	mux.HandleFunc("GET /network/{netID}/node/{device}/history", s.handleReadHistory)
	mux.HandleFunc("POST /network/{netID}/node/{device}/rollback-history", s.handleRollbackHistory)

	// Device settings
	mux.HandleFunc("GET /network/{netID}/node/{device}/settings", s.handleReadSettings)
	mux.HandleFunc("PUT /network/{netID}/node/{device}/settings", s.handleWriteSettings)

	// Drift detection
	mux.HandleFunc("GET /network/{netID}/node/{device}/drift", s.handleDetectDrift)
	mux.HandleFunc("GET /network/{netID}/node/{device}/topology/drift", s.handleDetectTopologyDrift)
	mux.HandleFunc("GET /network/{netID}/node/{device}/topology/intents", s.handleTopologyIntents)

	// ====================================================================
	// Node composite operations
	// ====================================================================
	mux.HandleFunc("POST /network/{netID}/node/{device}/generate-composite", s.handleCompositeGenerate)
	mux.HandleFunc("POST /network/{netID}/node/{device}/verify-composite", s.handleCompositeVerify)
	mux.HandleFunc("POST /network/{netID}/node/{device}/deliver-composite", s.handleCompositeDeliver)

	// ====================================================================
	// Interface operations
	// ====================================================================
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/apply-service", s.handleApplyService)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/remove-service", s.handleRemoveService)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/refresh-service", s.handleRefreshService)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/unconfigure-interface", s.handleUnconfigureInterface)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/bind-acl", s.handleBindACL)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/unbind-acl", s.handleUnbindACL)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/add-bgp-peer", s.handleAddBGPPeer)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/remove-bgp-peer", s.handleRemoveBGPPeer)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/set-property", s.handleInterfaceSet)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/clear-property", s.handleClearProperty)
	mux.HandleFunc("POST /network/{netID}/node/{device}/interface/{name}/configure-interface", s.handleConfigureInterface)
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
// Returns nil (no error) if the body is empty or absent — handlers with
// optional request bodies work correctly when called with no payload.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(v)
	if err == io.EOF {
		return nil
	}
	return err
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


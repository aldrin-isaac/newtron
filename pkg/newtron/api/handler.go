package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// buildMux creates the HTTP mux with all routes registered.
func (s *Server) buildMux() http.Handler {
	mux := http.NewServeMux()

	// ====================================================================
	// Server management
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/network", s.handleRegisterNetwork)
	mux.HandleFunc("GET /newtron/v1/network", s.handleListNetworks)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/unregister", s.handleUnregisterNetwork)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/reload", s.handleReloadNetwork)

	// ====================================================================
	// Network spec reads
	// ====================================================================
	mux.HandleFunc("GET /newtron/v1/network/{netID}/service", s.handleListServices)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/service/{name}", s.handleShowService)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/service/{name}/projection", s.handleServiceProjection)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/ipvpn", s.handleListIPVPNs)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/ipvpn/{name}", s.handleShowIPVPN)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/macvpn", s.handleListMACVPNs)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/macvpn/{name}", s.handleShowMACVPN)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/qos-policy", s.handleListQoSPolicies)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/qos-policy/{name}", s.handleShowQoSPolicy)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/filter", s.handleListFilters)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/filter/{name}", s.handleShowFilter)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/platform", s.handleListPlatforms)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/platform/{name}", s.handleShowPlatform)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/route-policy", s.handleListRoutePolicies)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/route-policy/{name}", s.handleShowRoutePolicy)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/prefix-list", s.handleListPrefixLists)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/prefix-list/{name}", s.handleShowPrefixList)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/topology", s.handleTopology)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/topology/node", s.handleTopologyDeviceNames)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/topology/create-node", s.handleCreateTopologyNode)
	mux.HandleFunc("DELETE /newtron/v1/network/{netID}/topology/node/{name}", s.handleDeleteTopologyNode)
	mux.HandleFunc("PUT /newtron/v1/network/{netID}/topology/node/{name}", s.handleUpdateTopologyNode)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/topology/create-link", s.handleCreateTopologyLink)
	mux.HandleFunc("DELETE /newtron/v1/network/{netID}/topology/link/{device}/{interface}", s.handleDeleteTopologyLink)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/host/{name}", s.handleGetHostProfile)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/feature", s.handleGetAllFeatures)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/feature/{name}/dependency", s.handleGetFeatureDependencies)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/feature/{name}/unsupported-due-to", s.handleGetUnsupportedDueTo)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/platform/{name}/supports/{feature}", s.handlePlatformSupportsFeature)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/profile", s.handleListProfiles)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/profile/{name}", s.handleShowProfile)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/zone", s.handleListZones)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/zone/{name}", s.handleShowZone)

	// ====================================================================
	// Network spec writes (RPC-style: verb in URL, POST for all writes)
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/network/{netID}/create-service", s.handleCreateService)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/delete-service", s.handleDeleteService)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/create-ipvpn", s.handleCreateIPVPN)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/delete-ipvpn", s.handleDeleteIPVPN)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/create-macvpn", s.handleCreateMACVPN)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/delete-macvpn", s.handleDeleteMACVPN)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/create-qos-policy", s.handleCreateQoSPolicy)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/delete-qos-policy", s.handleDeleteQoSPolicy)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/add-qos-queue", s.handleAddQoSQueue)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/remove-qos-queue", s.handleRemoveQoSQueue)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/create-filter", s.handleCreateFilter)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/delete-filter", s.handleDeleteFilter)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/add-filter-rule", s.handleAddFilterRule)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/remove-filter-rule", s.handleRemoveFilterRule)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/create-prefix-list", s.handleCreatePrefixList)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/delete-prefix-list", s.handleDeletePrefixList)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/add-prefix-list-entry", s.handleAddPrefixListEntry)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/remove-prefix-list-entry", s.handleRemovePrefixListEntry)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/create-route-policy", s.handleCreateRoutePolicy)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/delete-route-policy", s.handleDeleteRoutePolicy)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/add-route-policy-rule", s.handleAddRoutePolicyRule)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/remove-route-policy-rule", s.handleRemoveRoutePolicyRule)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/create-profile", s.handleCreateProfile)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/delete-profile", s.handleDeleteProfile)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/create-zone", s.handleCreateZone)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/delete-zone", s.handleDeleteZone)

	// ====================================================================
	// Device initialization
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/init-device", s.handleInitDevice)

	// ====================================================================
	// Node read operations
	// ====================================================================
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/info", s.handleNodeInfo)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/interface", s.handleListInterfaces)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/interface/{name}", s.handleShowInterface)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/interface/{name}/binding", s.handleShowServiceBinding)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/vlan", s.handleListVLANs)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/vlan/{id}", s.handleShowVLAN)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/vrf", s.handleListVRFs)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/vrf/{name}", s.handleShowVRF)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/acl", s.handleListACLs)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/acl/{name}", s.handleShowACL)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/bgp/status", s.handleBGPStatus)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/evpn/status", s.handleEVPNStatus)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/health", s.handleHealthCheck)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/lag", s.handleListLAGs)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/neighbor", s.handleListNeighbors)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/route/{vrf}/{prefix...}", s.handleGetRoute)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/route-asic/{prefix...}", s.handleGetRouteASIC)

	// ====================================================================
	// Node write operations (RPC-style: verb in URL, POST for all writes)
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/bind-macvpn", s.handleNodeBindMACVPN)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/unbind-macvpn", s.handleNodeUnbindMACVPN)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/reload-config", s.handleReloadConfig)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/save-config", s.handleSaveConfig)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/ssh-command", s.handleSSHCommand)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/create-vlan", s.handleCreateVLAN)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/delete-vlan", s.handleDeleteVLAN)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/configure-irb", s.handleConfigureIRB)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/unconfigure-irb", s.handleUnconfigureIRB)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/create-vrf", s.handleCreateVRF)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/delete-vrf", s.handleDeleteVRF)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/bind-ipvpn", s.handleBindIPVPN)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/unbind-ipvpn", s.handleUnbindIPVPN)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/add-static-route", s.handleAddStaticRoute)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/remove-static-route", s.handleRemoveStaticRoute)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/create-acl", s.handleCreateACL)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/delete-acl", s.handleDeleteACL)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/add-acl-rule", s.handleAddACLRule)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/remove-acl-rule", s.handleRemoveACLRule)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/create-portchannel", s.handleCreatePortChannel)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/delete-portchannel", s.handleDeletePortChannel)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/add-portchannel-member", s.handleAddPortChannelMember)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/remove-portchannel-member", s.handleRemovePortChannelMember)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/add-bgp-evpn-peer", s.handleAddBGPEVPNPeer)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/remove-bgp-evpn-peer", s.handleRemoveBGPEVPNPeer)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/restart-daemon", s.handleRestartDaemon)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/setup-device", s.handleSetupDevice)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/configdb", s.handleConfigDBSnapshot)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/configdb/{table}", s.handleConfigDBTableKeys)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/configdb/{table}/{key}", s.handleQueryConfigDB)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/configdb/{table}/{key}/exists", s.handleConfigDBEntryExists)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/statedb/{table}/{key}", s.handleQueryStateDB)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/bgp/check", s.handleCheckBGPSessions)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/lag/{name}", s.handleShowLAGDetail)

	// ====================================================================
	// Intent operations
	// ====================================================================
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/intent/projection", s.handleProjection)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/intent/projection-diff", s.handleProjectionDiff)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/intent/tree", s.handleTree)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/status", s.handleNodeStatus)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/intent/drift", s.handleDrift)
	mux.HandleFunc("GET /newtron/v1/network/{netID}/node/{device}/intent/topology-drift", s.handleTopologyDrift)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/intent/reconcile", s.handleReconcile)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/intent/save", s.handleSave)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/intent/reload", s.handleReload)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/intent/clear", s.handleClear)

	// ====================================================================
	// Interface operations
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/apply-service", s.handleApplyService)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/remove-service", s.handleRemoveService)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/refresh-service", s.handleRefreshService)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/unconfigure-interface", s.handleUnconfigureInterface)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/bind-acl", s.handleBindACL)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/unbind-acl", s.handleUnbindACL)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/add-bgp-peer", s.handleAddBGPPeer)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/remove-bgp-peer", s.handleRemoveBGPPeer)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/set-property", s.handleInterfaceSet)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/clear-property", s.handleClearProperty)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/configure-interface", s.handleConfigureInterface)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/apply-qos", s.handleApplyInterfaceQoS)
	mux.HandleFunc("POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/remove-qos", s.handleRemoveInterfaceQoS)

	// Apply middleware chain: recovery → logger → requestID → timeout → persist → mode → mux
	var handler http.Handler = mux
	handler = withMode(handler)
	handler = withPersist(handler)
	handler = httputil.Timeout(5 * time.Minute)(handler)
	handler = httputil.RequestID(handler)
	handler = httputil.Logger(s.logger)(handler)
	handler = httputil.Recovery(s.logger)(handler)

	return handler
}

// ============================================================================
// JSON helpers
// ============================================================================

// writeError writes a JSON error response.
//
// For VerificationFailedError, the typed WriteResult (Verification, DeviceOps,
// Changes) is propagated as the Data field of the envelope so consumers see
// the full substrate that newtron computed — §46 (HTTP API Boundary) on the
// failure path. Other error kinds emit Error only.
func writeError(w http.ResponseWriter, err error) {
	status := httpStatusFromError(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	envelope := httputil.APIResponse{Error: err.Error()}
	var verFailed *newtron.VerificationFailedError
	if errors.As(err, &verFailed) && verFailed.Result != nil {
		envelope.Data = verFailed.Result
	}
	json.NewEncoder(w).Encode(envelope)
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

// requireNetwork looks up the networkEntity or writes a 404.
func (s *Server) requireNetwork(w http.ResponseWriter, r *http.Request) *networkEntity {
	netID := r.PathValue("netID")
	ne := s.getNetwork(netID)
	if ne == nil {
		writeError(w, &notRegisteredError{netID})
		return nil
	}
	return ne
}

// requireNodeActor looks up the networkEntity and NodeActor, or writes an error.
func (s *Server) requireNodeActor(w http.ResponseWriter, r *http.Request) (*networkEntity, *NodeActor) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return nil, nil
	}
	device := r.PathValue("device")
	return ne, ne.getNodeActor(device)
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


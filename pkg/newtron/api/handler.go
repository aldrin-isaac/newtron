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
	mux.HandleFunc("POST /newtron/v1/networks", s.handleRegisterNetwork)
	mux.HandleFunc("GET /newtron/v1/networks", s.handleListNetworks)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/unregister", s.handleUnregisterNetwork)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/reload", s.handleReloadNetwork)

	// ====================================================================
	// Network spec reads
	// ====================================================================
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/services", s.handleListServices)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/services/{name}", s.handleShowService)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/services/{name}/projection", s.handleServiceProjection)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/ipvpns", s.handleListIPVPNs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/ipvpns/{name}", s.handleShowIPVPN)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/macvpns", s.handleListMACVPNs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/macvpns/{name}", s.handleShowMACVPN)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/qos-policies", s.handleListQoSPolicies)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/qos-policies/{name}", s.handleShowQoSPolicy)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/filters", s.handleListFilters)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/filters/{name}", s.handleShowFilter)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/platforms", s.handleListPlatforms)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/platforms/{name}", s.handleShowPlatform)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/route-policies", s.handleListRoutePolicies)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/route-policies/{name}", s.handleShowRoutePolicy)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/prefix-lists", s.handleListPrefixLists)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/prefix-lists/{name}", s.handleShowPrefixList)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/topology", s.handleTopology)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/topology/nodes", s.handleTopologyDeviceNames)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/topology/create-node", s.handleCreateTopologyNode)
	mux.HandleFunc("DELETE /newtron/v1/networks/{netID}/topology/nodes/{name}", s.handleDeleteTopologyNode)
	mux.HandleFunc("PUT /newtron/v1/networks/{netID}/topology/nodes/{name}", s.handleUpdateTopologyNode)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/topology/create-link", s.handleCreateTopologyLink)
	mux.HandleFunc("DELETE /newtron/v1/networks/{netID}/topology/links/{device}/{interface}", s.handleDeleteTopologyLink)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/hosts/{name}", s.handleGetHostProfile)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/features", s.handleGetAllFeatures)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/features/{name}/dependencies", s.handleGetFeatureDependencies)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/features/{name}/unsupported-due-to", s.handleGetUnsupportedDueTo)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/platforms/{name}/supports/{feature}", s.handlePlatformSupportsFeature)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/profiles", s.handleListProfiles)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/profiles/{name}", s.handleShowProfile)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/zones", s.handleListZones)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/zones/{name}", s.handleShowZone)

	// ====================================================================
	// Network spec writes (RPC-style: verb in URL, POST for all writes)
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-service", s.handleCreateService)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-service", s.handleDeleteService)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-ipvpn", s.handleCreateIPVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-ipvpn", s.handleDeleteIPVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-macvpn", s.handleCreateMACVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-macvpn", s.handleDeleteMACVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-qos-policy", s.handleCreateQoSPolicy)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-qos-policy", s.handleDeleteQoSPolicy)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/add-qos-queue", s.handleAddQoSQueue)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/remove-qos-queue", s.handleRemoveQoSQueue)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-filter", s.handleCreateFilter)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-filter", s.handleDeleteFilter)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/add-filter-rule", s.handleAddFilterRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/remove-filter-rule", s.handleRemoveFilterRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-prefix-list", s.handleCreatePrefixList)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-prefix-list", s.handleDeletePrefixList)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/add-prefix-list-entry", s.handleAddPrefixListEntry)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/remove-prefix-list-entry", s.handleRemovePrefixListEntry)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-route-policy", s.handleCreateRoutePolicy)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-route-policy", s.handleDeleteRoutePolicy)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/add-route-policy-rule", s.handleAddRoutePolicyRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/remove-route-policy-rule", s.handleRemoveRoutePolicyRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-profile", s.handleCreateProfile)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-profile", s.handleDeleteProfile)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-zone", s.handleCreateZone)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-zone", s.handleDeleteZone)

	// ====================================================================
	// Device initialization
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/init-device", s.handleInitDevice)

	// ====================================================================
	// Node read operations
	// ====================================================================
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/info", s.handleNodeInfo)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/interfaces", s.handleListInterfaces)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}", s.handleShowInterface)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/binding", s.handleShowServiceBinding)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/vlans", s.handleListVLANs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/vlans/{id}", s.handleShowVLAN)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/vrfs", s.handleListVRFs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/vrfs/{name}", s.handleShowVRF)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/acls", s.handleListACLs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/acls/{name}", s.handleShowACL)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/bgp/status", s.handleBGPStatus)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/evpn/status", s.handleEVPNStatus)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/health", s.handleHealthCheck)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/lags", s.handleListLAGs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/neighbors", s.handleListNeighbors)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/routes/{vrf}/{prefix...}", s.handleGetRoute)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/routes-asic/{prefix...}", s.handleGetRouteASIC)

	// ====================================================================
	// Node write operations (RPC-style: verb in URL, POST for all writes)
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/bind-macvpn", s.handleNodeBindMACVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/unbind-macvpn", s.handleNodeUnbindMACVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/reload-config", s.handleReloadConfig)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/save-config", s.handleSaveConfig)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/ssh-command", s.handleSSHCommand)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/create-vlan", s.handleCreateVLAN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/delete-vlan", s.handleDeleteVLAN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/configure-irb", s.handleConfigureIRB)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/unconfigure-irb", s.handleUnconfigureIRB)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/create-vrf", s.handleCreateVRF)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/delete-vrf", s.handleDeleteVRF)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/bind-ipvpn", s.handleBindIPVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/unbind-ipvpn", s.handleUnbindIPVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/add-static-route", s.handleAddStaticRoute)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/remove-static-route", s.handleRemoveStaticRoute)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/create-acl", s.handleCreateACL)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/delete-acl", s.handleDeleteACL)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/add-acl-rule", s.handleAddACLRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/remove-acl-rule", s.handleRemoveACLRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/create-portchannel", s.handleCreatePortChannel)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/delete-portchannel", s.handleDeletePortChannel)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/add-portchannel-member", s.handleAddPortChannelMember)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/remove-portchannel-member", s.handleRemovePortChannelMember)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/add-bgp-evpn-peer", s.handleAddBGPEVPNPeer)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/remove-bgp-evpn-peer", s.handleRemoveBGPEVPNPeer)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/restart-daemon", s.handleRestartDaemon)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/setup-device", s.handleSetupDevice)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/configdb", s.handleConfigDBSnapshot)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/configdb/{table}", s.handleConfigDBTableKeys)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/configdb/{table}/{key}", s.handleQueryConfigDB)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/configdb/{table}/{key}/exists", s.handleConfigDBEntryExists)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/statedb/{table}/{key}", s.handleQueryStateDB)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/bgp/check", s.handleCheckBGPSessions)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/lags/{name}", s.handleShowLAGDetail)

	// ====================================================================
	// Intent operations
	// ====================================================================
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/intent/projection", s.handleProjection)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/intent/projection-diff", s.handleProjectionDiff)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/intent/tree", s.handleTree)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/status", s.handleNodeStatus)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/intent/drift", s.handleDrift)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{device}/intent/topology-drift", s.handleTopologyDrift)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/intent/reconcile", s.handleReconcile)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/intent/save", s.handleSave)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/intent/reload", s.handleReload)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/intent/clear", s.handleClear)

	// ====================================================================
	// Interface operations
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/apply-service", s.handleApplyService)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/remove-service", s.handleRemoveService)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/refresh-service", s.handleRefreshService)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/unconfigure-interface", s.handleUnconfigureInterface)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/bind-acl", s.handleBindACL)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/unbind-acl", s.handleUnbindACL)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/add-bgp-peer", s.handleAddBGPPeer)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/remove-bgp-peer", s.handleRemoveBGPPeer)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/set-property", s.handleInterfaceSet)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/clear-property", s.handleClearProperty)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/configure-interface", s.handleConfigureInterface)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/apply-qos", s.handleApplyInterfaceQoS)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/remove-qos", s.handleRemoveInterfaceQoS)

	// Apply middleware chain (outermost → innermost):
	//   recovery → logger → requestID → pam → caller → audit → timeout → persist → mode → mux
	//
	// pam (auth-design.md L2b) sits between requestID and caller so
	// the verified PAM username is on the context for callerMiddleware
	// to pick up. PAMMiddleware is always installed; when no
	// Authenticator is configured (L2b disabled), it is a transparent
	// passthrough that doesn't touch the request.
	//
	// caller (auth-design.md L1+L2a+L2b) populates audit.Caller on the
	// request context before audit reads it. Always installed; effect
	// gated by which identity sources are configured per the
	// "every layer enable/disable-able" contract.
	var handler http.Handler = mux
	handler = withMode(handler)
	handler = withPersist(handler)
	handler = httputil.Timeout(5 * time.Minute)(handler)
	handler = auditMiddleware(handler)
	handler = callerMiddleware(s.auditCallerHeader)(handler)
	handler = httputil.PAMMiddleware(s.authenticator)(handler)
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
// For typed errors that carry actionable substrate, the typed payload is
// propagated as the Data field of the envelope so consumers see what newtron
// computed — §46 (HTTP API Boundary) on the failure path. Today two error
// kinds carry typed Data:
//
//   - VerificationFailedError → WriteResult (Verification, DeviceOps, Changes)
//   - alreadyRegisteredError → AlreadyRegisteredErrorInfo (existing spec_dir)
//
// Other error kinds emit Error only.
func writeError(w http.ResponseWriter, err error) {
	status := httpStatusFromError(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	envelope := httputil.APIResponse{Error: err.Error()}
	var verFailed *newtron.VerificationFailedError
	if errors.As(err, &verFailed) && verFailed.Result != nil {
		envelope.Data = verFailed.Result
	}
	var alreadyReg *alreadyRegisteredError
	if errors.As(err, &alreadyReg) {
		envelope.Data = AlreadyRegisteredErrorInfo{
			ID:              alreadyReg.id,
			ExistingSpecDir: alreadyReg.existingSpecDir,
		}
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

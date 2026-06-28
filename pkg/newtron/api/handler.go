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
	mux.HandleFunc("POST /newtron/v1/networks", s.handleCreateNetwork)
	mux.HandleFunc("GET /newtron/v1/networks", s.handleListNetworks)

	// ====================================================================
	// Schema metadata — human labels, tooltips, enums, refs for spec
	// authoring UIs (see handler_schema.go)
	// ====================================================================
	mux.HandleFunc("GET /newtron/v1/schema", s.handleSchemaList)
	mux.HandleFunc("GET /newtron/v1/schema/all", s.handleSchemaAll)
	mux.HandleFunc("GET /newtron/v1/schema/{kind}", s.handleSchemaShow)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/unregister", s.handleUnregisterNetwork)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/reload", s.handleReloadNetwork)

	// Write-control reservation (per network). request/relinquish are
	// permissioned (control.request / control.takeover) and audited like any
	// mutation; status is an open read.
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/control", s.handleControlStatus)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/control/request", s.handleControlRequest)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/control/relinquish", s.handleControlRelinquish)

	// Auth routes (POST /newt-server/v1/auth/login, POST
	// /newt-server/v1/auth/logout) live at the server boundary in
	// cmd/newt-server, not in the newtron engine. Identity reaches
	// downstream handlers via the request context, populated by
	// outer middleware (sessionkey.Middleware for L2c Bearer tokens,
	// httputil.PAMMiddleware for L2b Basic auth). callerMiddleware
	// reads the verified username from context regardless of which
	// outer layer attached it.

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
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/spec-instances", s.handleSpecInstances)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/topology", s.handleTopology)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/topology/nodes", s.handleTopologyDeviceNames)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/topology/create-node", s.handleCreateTopologyNode)
	mux.HandleFunc("DELETE /newtron/v1/networks/{netID}/topology/nodes/{name}", s.handleDeleteTopologyNode)
	mux.HandleFunc("PUT /newtron/v1/networks/{netID}/topology/nodes/{name}", s.handleUpdateTopologyNode)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/topology/create-link", s.handleCreateTopologyLink)
	mux.HandleFunc("DELETE /newtron/v1/networks/{netID}/topology/links/{node}/{interface}", s.handleDeleteTopologyLink)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/host-connection", s.handleHostConnection)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/features", s.handleGetAllFeatures)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/features/{name}/dependencies", s.handleGetFeatureDependencies)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/features/{name}/unsupported-due-to", s.handleGetUnsupportedDueTo)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/platforms/{name}/supports/{feature}", s.handlePlatformSupportsFeature)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes", s.handleListNodeSpecs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{name}", s.handleShowNodeSpec)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/zones", s.handleListZones)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/zones/{name}", s.handleShowZone)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/authorization", s.handleGetAuthorization)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/audit/events", s.handleAuditEvents)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/audit/events/{eventID}", s.handleAuditEvent)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/audit/integrity", s.handleAuditIntegrity)

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
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-qos-queue", s.handleUpdateQoSQueue)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/remove-qos-queue", s.handleRemoveQoSQueue)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-filter", s.handleCreateFilter)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-filter", s.handleDeleteFilter)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/add-filter-rule", s.handleAddFilterRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-filter-rule", s.handleUpdateFilterRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/remove-filter-rule", s.handleRemoveFilterRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-prefix-list", s.handleCreatePrefixList)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-prefix-list", s.handleDeletePrefixList)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/add-prefix-list-entry", s.handleAddPrefixListEntry)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/remove-prefix-list-entry", s.handleRemovePrefixListEntry)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-route-policy", s.handleCreateRoutePolicy)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-route-policy", s.handleDeleteRoutePolicy)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/add-route-policy-rule", s.handleAddRoutePolicyRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-route-policy-rule", s.handleUpdateRoutePolicyRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/remove-route-policy-rule", s.handleRemoveRoutePolicyRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-node", s.handleCreateNodeSpec)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-node", s.handleDeleteNodeSpec)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/create-zone", s.handleCreateZone)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/delete-zone", s.handleDeleteZone)
	// Platform CRUD wire endpoints removed: platforms are a global
	// registry authored on-disk under --platforms-base.

	// Update verbs (#152) — full-replacement spec mutation, parallel
	// to create-X/delete-X. Same request shape as create.
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-service", s.handleUpdateService)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-ipvpn", s.handleUpdateIPVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-macvpn", s.handleUpdateMACVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-qos-policy", s.handleUpdateQoSPolicy)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-filter", s.handleUpdateFilter)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-prefix-list", s.handleUpdatePrefixList)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-route-policy", s.handleUpdateRoutePolicy)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-node", s.handleUpdateNodeSpec)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/update-zone", s.handleUpdateZone)

	// ====================================================================
	// Device initialization
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/init-device", s.handleInitDevice)

	// ====================================================================
	// Node read operations
	// ====================================================================
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/info", s.handleNodeInfo)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/interfaces", s.handleListInterfaces)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}", s.handleShowInterface)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/binding", s.handleShowServiceBinding)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/vlans", s.handleListVLANs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/vlans/{id}", s.handleShowVLAN)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/vrfs", s.handleListVRFs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/vrfs/{name}", s.handleShowVRF)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/acls", s.handleListACLs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/acls/{name}", s.handleShowACL)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/bgp/status", s.handleBGPStatus)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/evpn/status", s.handleEVPNStatus)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/health", s.handleHealthCheck)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/lags", s.handleListLAGs)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/neighbors", s.handleListNeighbors)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/routes/{vrf}/{prefix...}", s.handleGetRoute)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/routes-asic/{prefix...}", s.handleGetRouteASIC)

	// ====================================================================
	// Node write operations (RPC-style: verb in URL, POST for all writes)
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/bind-macvpn", s.handleNodeBindMACVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/unbind-macvpn", s.handleNodeUnbindMACVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/reload-config", s.handleReloadConfig)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/save-config", s.handleSaveConfig)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/ssh-command", s.handleSSHCommand)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/create-vlan", s.handleCreateVLAN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/delete-vlan", s.handleDeleteVLAN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/configure-irb", s.handleConfigureIRB)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/unconfigure-irb", s.handleUnconfigureIRB)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/create-vrf", s.handleCreateVRF)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/delete-vrf", s.handleDeleteVRF)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/bind-ipvpn", s.handleBindIPVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/unbind-ipvpn", s.handleUnbindIPVPN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/add-static-route", s.handleAddStaticRoute)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/update-static-route", s.handleUpdateStaticRoute)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/remove-static-route", s.handleRemoveStaticRoute)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/create-acl", s.handleCreateACL)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/delete-acl", s.handleDeleteACL)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/add-acl-rule", s.handleAddACLRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/update-acl-rule", s.handleUpdateACLRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/remove-acl-rule", s.handleRemoveACLRule)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/create-portchannel", s.handleCreatePortChannel)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/delete-portchannel", s.handleDeletePortChannel)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/add-portchannel-member", s.handleAddPortChannelMember)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/remove-portchannel-member", s.handleRemovePortChannelMember)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/add-bgp-evpn-peer", s.handleAddBGPEVPNPeer)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/update-bgp-evpn-peer", s.handleUpdateBGPEVPNPeer)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/remove-bgp-evpn-peer", s.handleRemoveBGPEVPNPeer)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/restart-daemon", s.handleRestartDaemon)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/setup-device", s.handleSetupDevice)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/configdb", s.handleConfigDBSnapshot)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/configdb/{table}", s.handleConfigDBTableKeys)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/configdb/{table}/{key}", s.handleQueryConfigDB)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/configdb/{table}/{key}/exists", s.handleConfigDBEntryExists)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/statedb/{table}/{key}", s.handleQueryStateDB)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/bgp/check", s.handleCheckBGPSessions)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/lags/{name}", s.handleShowLAGDetail)

	// ====================================================================
	// Intent operations
	// ====================================================================
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/intent/projection", s.handleProjection)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/intent/projection-diff", s.handleProjectionDiff)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/intent/tree", s.handleTree)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/status", s.handleNodeStatus)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/intent/drift", s.handleDrift)
	mux.HandleFunc("GET /newtron/v1/networks/{netID}/nodes/{node}/intent/topology-drift", s.handleTopologyDrift)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/intent/reconcile", s.handleReconcile)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/intent/save", s.handleSave)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/intent/reload", s.handleReload)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/intent/clear", s.handleClear)

	// ====================================================================
	// Interface operations
	// ====================================================================
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/apply-service", s.handleApplyService)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/remove-service", s.handleRemoveService)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/refresh-service", s.handleRefreshService)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/unconfigure-interface", s.handleUnconfigureInterface)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/bind-acl", s.handleBindACL)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/unbind-acl", s.handleUnbindACL)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/add-bgp-peer", s.handleAddBGPPeer)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/update-bgp-peer", s.handleUpdateBGPPeer)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/remove-bgp-peer", s.handleRemoveBGPPeer)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/set-property", s.handleInterfaceSet)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/clear-property", s.handleClearProperty)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/configure-interface", s.handleConfigureInterface)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/remove-trunk-vlan", s.handleRemoveTrunkVLAN)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/bind-qos", s.handleBindQoS)
	mux.HandleFunc("POST /newtron/v1/networks/{netID}/nodes/{node}/interfaces/{name}/unbind-qos", s.handleUnbindQoS)

	// Apply middleware chain (outermost → innermost):
	//   recovery → logger → requestID → caller → audit → timeout → persist → mode → mux
	//
	// Identity middleware (sessionkey.Middleware for L2c Bearer,
	// httputil.PAMMiddleware for L2b Basic) lives at the server
	// boundary in cmd/newt-server, NOT here. The standalone
	// newtron-server has no identity middleware — it is a dev tool
	// reachable on loopback. Either way, callerMiddleware reads the
	// verified username off the request context regardless of who
	// attached it (or attaches the self-attested
	// X-Newtron-Caller header value when no outer layer ran).
	var handler http.Handler = mux
	handler = withMode(handler)
	handler = withPersist(handler)
	// Inside auditMiddleware (so a refused write is recorded) and inside
	// callerMiddleware (so the caller identity is in context).
	handler = s.withWriteControl(handler)
	handler = httputil.Timeout(5 * time.Minute)(handler)
	handler = auditMiddleware(handler)
	handler = callerMiddleware(s.auditCallerHeader)(handler)
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
//   - AuthorizationError → the AuthorizationError itself (Caller, Permission,
//     Resource) per auth-design.md L3
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
	var authz *newtron.AuthorizationError
	if errors.As(err, &authz) {
		envelope.Data = authz
	}
	// §46: a referential conflict carries its structured shape (resource, name,
	// references[], force_available) in Data so clients branch on the payload —
	// not on a parsed message — and render referrers + a force affordance only
	// when force_available is true.
	var conflict *newtron.ConflictError
	if errors.As(err, &conflict) {
		envelope.Data = conflict
	}
	// Write-control refusal carries {network, holder, since, last_active} in Data
	// so a client renders "alice holds write control since … — relinquish, take
	// over, or wait" without parsing the message.
	var wcErr *newtron.WriteControlError
	if errors.As(err, &wcErr) {
		envelope.Data = wcErr
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
	device := r.PathValue("node")
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

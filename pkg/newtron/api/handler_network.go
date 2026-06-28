package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// idPattern is the canonical network ID validator. Letters, digits,
// underscore, hyphen; 1–64 characters. No path separators, no dots, no
// spaces — the id maps directly to a directory name under networksBase
// and to a URL path segment, so the surface stays unambiguous.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ============================================================================
// Server management
// ============================================================================

// handleCreateNetwork is the POST /newtron/v1/networks handler. The
// verb is "create" because that's the operator's intent in both
// outcomes the endpoint covers (make a new slot, or pick up an
// existing one and register it). Always idempotent — the status
// code distinguishes new (201) from already-existed (200).
func (s *Server) handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	var req CreateNetworkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.ID == "" {
		writeError(w, &newtron.ValidationError{Field: "id", Message: "required"})
		return
	}
	if !idPattern.MatchString(req.ID) {
		writeError(w, &newtron.ValidationError{
			Field:   "id",
			Message: "must match ^[A-Za-z0-9_-]{1,64}$ — letters, digits, '-', and '_' only",
		})
		return
	}

	// Already registered? Return its info with 200 (idempotent — the
	// slot is already in the state the caller asked for).
	if info := s.getNetworkInfo(req.ID); info != nil {
		httputil.WriteJSON(w, http.StatusOK, info)
		return
	}

	if err := s.CreateNetwork(req.ID, req.Description); err != nil {
		writeError(w, err)
		return
	}
	info := s.getNetworkInfo(req.ID)
	if info == nil {
		// Create succeeded but the entity vanished between Unlock
		// and the getNetworkInfo RLock — concurrent unregister.
		// Surface as 500; the caller can retry.
		writeError(w, fmt.Errorf("network %q registered but no longer present (concurrent unregister?)", req.ID))
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, info)
}

// dirHasSpecs returns true when `dir` looks like a registered-existing
// network slot rather than an empty / missing path. The marker we
// trust is `network.json` — every scaffolded network writes one at
// creation, and every registered-existing network is loaded through it.
func dirHasSpecs(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "network.json"))
	return err == nil
}

func (s *Server) handleListNetworks(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.ListNetworks())
}

func (s *Server) handleUnregisterNetwork(w http.ResponseWriter, r *http.Request) {
	netID := r.PathValue("netID")
	if err := s.UnregisterNetwork(netID); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "unregistered"})
}

func (s *Server) handleReloadNetwork(w http.ResponseWriter, r *http.Request) {
	netID := r.PathValue("netID")
	if err := s.ReloadNetwork(netID); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

// ============================================================================
// Network spec reads
// ============================================================================

// handleSpecInstances returns the flat cross-scope spec inventory: every spec
// defined at network/zone/node, each tagged with scope + scope_instance. Additive
// and read-only — the per-kind list endpoints below (network scope) are
// unchanged. A schema-driven UI renders one flat list filtered by the two scope
// dropdowns from this; storage stays hierarchical underneath.
func (s *Server) handleSpecInstances(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	instances, err := ne.net.ListSpecInstances()
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, instances)
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.ListServices())
}

func (s *Server) handleShowService(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	val, err := ne.net.ShowService(r.PathValue("name"))
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// handleServiceProjection returns the per-Node projection slices the named
// service contributes. Iterates over every NodeActor with a currently-built
// abstract node, asks each whose Node BindsService(name), and computes the
// per-Node slice via Node.ServiceProjection (replay-diff technique).
//
// §11 + §46: each per-Node slice is the canonical []sonic.DriftEntry
// vocabulary. Aggregated into *newtron.ServiceProjectionResult.
func (s *Server) handleServiceProjection(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	serviceName := r.PathValue("name")
	result := &newtron.ServiceProjectionResult{Service: serviceName}

	// Snapshot the NodeActor map so iteration doesn't race with new actors
	// being created mid-iteration.
	ne.nodeMu.Lock()
	actors := make(map[string]*NodeActor, len(ne.nodeActors))
	for name, a := range ne.nodeActors {
		actors[name] = a
	}
	ne.nodeMu.Unlock()

	deviceNames := make([]string, 0, len(actors))
	for name := range actors {
		deviceNames = append(deviceNames, name)
	}
	sort.Strings(deviceNames)

	for _, device := range deviceNames {
		actor := actors[device]
		val, err := actor.do(r.Context(), func() (any, error) {
			if actor.node == nil {
				return nil, nil // not currently built — skip
			}
			if !actor.node.BindsService(serviceName) {
				return nil, nil
			}
			return actor.node.ServiceProjection(r.Context(), serviceName)
		})
		if err != nil {
			writeError(w, fmt.Errorf("computing service projection for %s: %w", device, err))
			return
		}
		if val == nil {
			continue
		}
		diff, ok := val.([]sonic.DriftEntry)
		if !ok {
			writeError(w, fmt.Errorf("unexpected type from ServiceProjection: %T", val))
			return
		}
		result.Nodes = append(result.Nodes, newtron.ServiceProjectionNode{
			Node: device,
			Diff: diff,
		})
	}

	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Server) handleListIPVPNs(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.ListIPVPNs())
}

func (s *Server) handleShowIPVPN(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	val, err := ne.net.ShowIPVPN(r.PathValue("name"))
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleListMACVPNs(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.ListMACVPNs())
}

func (s *Server) handleShowMACVPN(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	val, err := ne.net.ShowMACVPN(r.PathValue("name"))
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleListQoSPolicies(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.ListQoSPolicies())
}

func (s *Server) handleShowQoSPolicy(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	val, err := ne.net.ShowQoSPolicy(r.PathValue("name"))
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleListFilters(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.ListFilters())
}

func (s *Server) handleShowFilter(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	val, err := ne.net.ShowFilter(r.PathValue("name"))
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// handleListPlatforms returns the platforms.json contents.
func (s *Server) handleListPlatforms(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.ListPlatforms())
}

func (s *Server) handleShowPlatform(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	val, err := ne.net.ShowPlatform(r.PathValue("name"))
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}


func (s *Server) handleListRoutePolicies(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.ListRoutePolicies())
}

func (s *Server) handleListPrefixLists(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.ListPrefixLists())
}

func (s *Server) handleTopologyDeviceNames(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.TopologyDeviceNames())
}

// handleTopology returns the full topology spec (devices + links + metadata)
// as `spec.TopologySpecFile`. §46: canonical substrate exposed directly,
// alongside the names-only summary at /topology/node.
func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	// Serve the enriched view: same shape as the on-disk topology spec file,
	// but each step carries server-derived spec_kind/spec_name so a client gets
	// spec provenance for the whole network in one call, lab-independent.
	topo := ne.net.TopologyView()
	if topo == nil {
		writeError(w, &newtron.NotFoundError{Resource: "topology", Name: ""})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, topo)
}

// ============================================================================
// Topology CRUD handlers — newtron#15 + #16 (Phase 5)
// ============================================================================

// handleCreateTopologyNode adds a device entry to topology.json. Body is
// {name, device: TopologyDevice}. 409 on duplicate name; 400 on missing
// profile / invalid body.
func (s *Server) handleCreateTopologyNode(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req TopologyNodeCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.Name == "" || req.Device == nil {
		writeError(w, &newtron.ValidationError{Message: "name and device required"})
		return
	}
	if err := ne.net.AddTopologyDevice(r.Context(), req.Name, req.Device); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, req.Device)
}

// handleDeleteTopologyNode removes a device entry from topology.json. URL
// path carries the name. Query param ?force=true cascade-deletes referring
// links. 409 (ConflictError) when references remain and force is absent.
// Also closes any api-layer NodeActor cache for this name (handler cleanup
// per Q4 design).
func (s *Server) handleDeleteTopologyNode(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	name := r.PathValue("name")
	force := r.URL.Query().Get("force") == "true"
	if err := ne.net.DeleteTopologyDevice(r.Context(), name, force); err != nil {
		writeError(w, err)
		return
	}
	ne.removeNodeActor(name) // clear stale cache; spec entry is gone
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"deleted": name})
}

// handleUpdateTopologyNode replaces the device entry at name with the body.
// Full-replacement semantics — body must be a complete TopologyDevice. 404
// when name doesn't exist. Closes the api-layer NodeActor cache so the next
// request rebuilds from the new spec.
func (s *Server) handleUpdateTopologyNode(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	name := r.PathValue("name")
	var device spec.TopologyDevice
	if err := decodeJSON(r, &device); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.UpdateTopologyDevice(r.Context(), name, &device); err != nil {
		writeError(w, err)
		return
	}
	ne.removeNodeActor(name) // built node now reflects stale spec
	httputil.WriteJSON(w, http.StatusOK, &device)
}

// handleCreateTopologyLink adds a link to topology.json. Body is the typed
// TopologyLink (a, z endpoint strings). 409 when either endpoint is already
// wired; 400 on validation failure.
func (s *Server) handleCreateTopologyLink(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var link spec.TopologyLink
	if err := decodeJSON(r, &link); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.AddTopologyLink(r.Context(), &link); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, &link)
}

// handleDeleteTopologyLink removes the link containing the given endpoint
// (URL path: /topology/link/{device}/{interface}). A port participates in at
// most one link, so a single endpoint uniquely identifies it. 404 when no
// link contains the endpoint.
func (s *Server) handleDeleteTopologyLink(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	device := r.PathValue("device")
	iface := r.PathValue("interface")
	if device == "" || iface == "" {
		writeError(w, &newtron.ValidationError{Message: "device and interface required in URL"})
		return
	}
	endpoint := device + ":" + iface
	if err := ne.net.DeleteTopologyLink(r.Context(), endpoint); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"deleted": endpoint})
}

func (s *Server) handleGetHostProfile(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	name := r.PathValue("name")
	// Only return profiles for actual host devices, not switches.
	// The client uses 200 vs 404 from this endpoint to classify devices.
	if !ne.net.IsHostDevice(name) {
		writeError(w, &newtron.NotFoundError{Resource: "host device", Name: name})
		return
	}
	val, err := ne.net.GetHostProfile(r.Context(), name)
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleGetAllFeatures(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.GetAllFeatures())
}

func (s *Server) handleGetFeatureDependencies(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.GetFeatureDependencies(r.PathValue("name")))
}

// ============================================================================
// Network spec writes
// ============================================================================

func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateServiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.CreateService(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	force := r.URL.Query().Get("force") == "true"
	if err := ne.net.DeleteService(r.Context(), req.ScopeSelector, req.Name, opts, force); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleCreateIPVPN(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateIPVPNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.CreateIPVPN(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteIPVPN(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	force := r.URL.Query().Get("force") == "true"
	if err := ne.net.DeleteIPVPN(r.Context(), req.ScopeSelector, req.Name, opts, force); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleCreateMACVPN(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateMACVPNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.CreateMACVPN(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteMACVPN(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	force := r.URL.Query().Get("force") == "true"
	if err := ne.net.DeleteMACVPN(r.Context(), req.ScopeSelector, req.Name, opts, force); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleCreateQoSPolicy(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateQoSPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.CreateQoSPolicy(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteQoSPolicy(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	force := r.URL.Query().Get("force") == "true"
	if err := ne.net.DeleteQoSPolicy(r.Context(), req.ScopeSelector, req.Name, opts, force); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAddQoSQueue(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.AddQoSQueueRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.AddQoSQueue(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]int{"queue_id": req.QueueID})
}

func (s *Server) handleUpdateQoSQueue(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.UpdateQoSQueueRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.UpdateQoSQueue(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	resultID := req.QueueID
	if req.NewQueueID != nil {
		resultID = *req.NewQueueID
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]int{"queue_id": resultID})
}

func (s *Server) handleRemoveQoSQueue(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		Policy  string `json:"policy"`
		QueueID int    `json:"queue_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.RemoveQoSQueue(r.Context(), req.ScopeSelector, req.Policy, req.QueueID, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleCreateFilter(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateFilterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.CreateFilter(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteFilter(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	force := r.URL.Query().Get("force") == "true"
	if err := ne.net.DeleteFilter(r.Context(), req.ScopeSelector, req.Name, opts, force); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAddFilterRule(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.AddFilterRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.AddFilterRule(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]int{"seq": req.Sequence})
}

func (s *Server) handleUpdateFilterRule(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.UpdateFilterRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.UpdateFilterRule(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	resultSeq := req.Sequence
	if req.NewSequence != nil {
		resultSeq = *req.NewSequence
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]int{"seq": resultSeq})
}

func (s *Server) handleRemoveFilterRule(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		Filter   string `json:"filter"`
		Sequence int    `json:"seq"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.RemoveFilterRule(r.Context(), req.ScopeSelector, req.Filter, req.Sequence, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ============================================================================
// Prefix Lists
// ============================================================================

func (s *Server) handleShowPrefixList(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	name := r.PathValue("name")
	val, err := ne.net.ShowPrefixList(name)
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleCreatePrefixList(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreatePrefixListRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.CreatePrefixList(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeletePrefixList(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.DeletePrefixList(r.Context(), req.ScopeSelector, req.Name, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAddPrefixListEntry(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.AddPrefixListEntryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.AddPrefixListEntry(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"prefix": req.Prefix})
}

func (s *Server) handleRemovePrefixListEntry(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		PrefixList string `json:"prefix_list"`
		Prefix     string `json:"prefix"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.RemovePrefixListEntry(r.Context(), req.ScopeSelector, req.PrefixList, req.Prefix, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ============================================================================
// Route Policies
// ============================================================================

func (s *Server) handleShowRoutePolicy(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	name := r.PathValue("name")
	val, err := ne.net.ShowRoutePolicy(name)
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleCreateRoutePolicy(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateRoutePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.CreateRoutePolicy(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteRoutePolicy(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.DeleteRoutePolicy(r.Context(), req.ScopeSelector, req.Name, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAddRoutePolicyRule(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.AddRoutePolicyRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.AddRoutePolicyRule(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]int{"seq": req.Sequence})
}

func (s *Server) handleUpdateRoutePolicyRule(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.UpdateRoutePolicyRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.UpdateRoutePolicyRule(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	resultSeq := req.Sequence
	if req.NewSequence != nil {
		resultSeq = *req.NewSequence
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]int{"seq": resultSeq})
}

func (s *Server) handleRemoveRoutePolicyRule(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		newtron.ScopeSelector
		Policy   string `json:"policy"`
		Sequence int    `json:"seq"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.RemoveRoutePolicyRule(r.Context(), req.ScopeSelector, req.Policy, req.Sequence, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ============================================================================
// Profiles
// ============================================================================

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.ListProfiles())
}

// handleShowProfile returns the device profile for a named device.
func (s *Server) handleShowProfile(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	name := r.PathValue("name")
	val, err := ne.net.ShowProfile(name)
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateDeviceProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.CreateProfile(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	force := r.URL.Query().Get("force") == "true"
	if err := ne.net.DeleteProfile(r.Context(), req.Name, opts, force); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ============================================================================
// Zones
// ============================================================================

func (s *Server) handleListZones(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.ListZones())
}

func (s *Server) handleShowZone(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	name := r.PathValue("name")
	val, err := ne.net.ShowZone(name)
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleCreateZone(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateZoneRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.CreateZone(r.Context(), req, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteZone(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	if err := ne.net.DeleteZone(r.Context(), req.Name, opts); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ============================================================================
// Platform feature support
// ============================================================================

func (s *Server) handlePlatformSupportsFeature(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	platform := r.PathValue("name")
	feature := r.PathValue("feature")
	httputil.WriteJSON(w, http.StatusOK, map[string]bool{"supported": ne.net.PlatformSupportsFeature(platform, feature)})
}

func (s *Server) handleGetUnsupportedDueTo(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.GetUnsupportedDueTo(r.PathValue("name")))
}

func (s *Server) handleInitDevice(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	// Body is optional — force defaults to false.
	_ = decodeJSON(r, &req)
	device := r.PathValue("device")
	if err := ne.net.InitDevice(r.Context(), device, req.Force); err != nil {
		if errors.Is(err, newtron.ErrAlreadyInitialized) {
			httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "already_initialized"})
			return
		}
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "initialized"})
}


// ============================================================================
// Update handlers (#152) — full-replacement spec mutation
// ============================================================================
//
// Each handler decodes the Create<Kind>Request shape (Update reuses
// the same wire form — name + fields), calls Update<Kind> on the
// public Network, and returns 200 with the entry name. 404 surfaces
// from the engine when the named entry does not exist; 403 from the
// auth gate. The execOpts wrapper preserves the ?execute=false dry-run
// semantic the Create/Delete handlers also honor.

func (s *Server) handleUpdateService(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateServiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.UpdateService(r.Context(), req, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}

func (s *Server) handleUpdateIPVPN(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateIPVPNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.UpdateIPVPN(r.Context(), req, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}

func (s *Server) handleUpdateMACVPN(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateMACVPNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.UpdateMACVPN(r.Context(), req, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}

func (s *Server) handleUpdateQoSPolicy(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateQoSPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.UpdateQoSPolicy(r.Context(), req, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}

func (s *Server) handleUpdateFilter(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateFilterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.UpdateFilter(r.Context(), req, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}

func (s *Server) handleUpdatePrefixList(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreatePrefixListRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.UpdatePrefixList(r.Context(), req, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}

func (s *Server) handleUpdateRoutePolicy(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateRoutePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.UpdateRoutePolicy(r.Context(), req, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateDeviceProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.UpdateProfile(r.Context(), req, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}

func (s *Server) handleUpdateZone(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req newtron.CreateZoneRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if err := ne.net.UpdateZone(r.Context(), req, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}

// Platform CRUD handlers removed: platforms are now a global registry
// authored on-disk under --platforms-base, not via wire. Matches the
// schema-metadata's existing "platforms are read-only via universal UI"
// declaration; adding a platform requires backend coordination.

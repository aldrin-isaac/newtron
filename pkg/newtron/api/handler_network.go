package api

import (
	"net/http"

	"github.com/newtron-network/newtron/pkg/newtron"
)

// ============================================================================
// Server management
// ============================================================================

func (s *Server) handleRegisterNetwork(w http.ResponseWriter, r *http.Request) {
	var req RegisterNetworkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.ID == "" || req.SpecDir == "" {
		writeError(w, &newtron.ValidationError{Message: "id and spec_dir are required"})
		return
	}
	if err := s.RegisterNetwork(req.ID, req.SpecDir); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": req.ID})
}

func (s *Server) handleListNetworks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.listNetworks())
}

func (s *Server) handleUnregisterNetwork(w http.ResponseWriter, r *http.Request) {
	netID := r.PathValue("netID")
	if err := s.UnregisterNetwork(netID); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unregistered"})
}

func (s *Server) handleReloadNetwork(w http.ResponseWriter, r *http.Request) {
	netID := r.PathValue("netID")
	if err := s.ReloadNetwork(netID); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

// ============================================================================
// Network spec reads
// ============================================================================

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ListServices(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowService(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ShowService(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleListIPVPNs(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ListIPVPNs(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowIPVPN(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ShowIPVPN(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleListMACVPNs(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ListMACVPNs(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowMACVPN(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ShowMACVPN(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleListQoSPolicies(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ListQoSPolicies(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowQoSPolicy(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ShowQoSPolicy(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleListFilters(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ListFilters(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowFilter(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ShowFilter(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleListPlatforms(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ListPlatforms(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowPlatform(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ShowPlatform(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleListRoutePolicies(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ListRoutePolicies(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleListPrefixLists(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ListPrefixLists(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleTopologyDeviceNames(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.TopologyDeviceNames(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleGetHostProfile(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	// Only return profiles for actual host devices, not switches.
	// The client uses 200 vs 404 from this endpoint to classify devices.
	if !na.net.IsHostDevice(name) {
		writeError(w, &newtron.NotFoundError{Resource: "host device", Name: name})
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.GetHostProfile(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleGetAllFeatures(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.GetAllFeatures(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleGetFeatureDependencies(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.GetFeatureDependencies(name), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

// ============================================================================
// Network spec writes
// ============================================================================

func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	var req newtron.CreateServiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.CreateService(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.DeleteService(name, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleCreateIPVPN(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	var req newtron.CreateIPVPNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.CreateIPVPN(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteIPVPN(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.DeleteIPVPN(name, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleCreateMACVPN(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	var req newtron.CreateMACVPNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.CreateMACVPN(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteMACVPN(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.DeleteMACVPN(name, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleCreateQoSPolicy(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	var req newtron.CreateQoSPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.CreateQoSPolicy(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteQoSPolicy(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.DeleteQoSPolicy(name, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAddQoSQueue(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	policy := r.PathValue("name")
	var req newtron.AddQoSQueueRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	req.Policy = policy
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.AddQoSQueue(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"queue_id": req.QueueID})
}

func (s *Server) handleRemoveQoSQueue(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	policy := r.PathValue("name")
	queueID, err := pathInt(r, "id")
	if err != nil {
		writeError(w, &newtron.ValidationError{Field: "id", Message: "invalid queue ID"})
		return
	}
	opts := execOpts(r)
	_, err = na.do(r.Context(), func() (any, error) {
		return nil, na.net.RemoveQoSQueue(policy, queueID, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleCreateFilter(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	var req newtron.CreateFilterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.CreateFilter(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteFilter(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.DeleteFilter(name, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAddFilterRule(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	filter := r.PathValue("name")
	var req newtron.AddFilterRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	req.Filter = filter
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.AddFilterRule(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"seq": req.Sequence})
}

func (s *Server) handleRemoveFilterRule(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	filter := r.PathValue("name")
	seq, err := pathInt(r, "seq")
	if err != nil {
		writeError(w, &newtron.ValidationError{Field: "seq", Message: "invalid sequence number"})
		return
	}
	opts := execOpts(r)
	_, err = na.do(r.Context(), func() (any, error) {
		return nil, na.net.RemoveFilterRule(filter, seq, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ============================================================================
// Prefix Lists
// ============================================================================

func (s *Server) handleShowPrefixList(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ShowPrefixList(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleCreatePrefixList(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	var req newtron.CreatePrefixListRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.CreatePrefixList(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeletePrefixList(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.DeletePrefixList(name, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAddPrefixListEntry(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	var req newtron.AddPrefixListEntryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	req.PrefixList = r.PathValue("name")
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.AddPrefixListEntry(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"prefix": req.Prefix})
}

func (s *Server) handleRemovePrefixListEntry(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	prefixList := r.PathValue("name")
	prefix := r.PathValue("prefix")
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.RemovePrefixListEntry(prefixList, prefix, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ============================================================================
// Route Policies
// ============================================================================

func (s *Server) handleShowRoutePolicy(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ShowRoutePolicy(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleCreateRoutePolicy(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	var req newtron.CreateRoutePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.CreateRoutePolicy(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (s *Server) handleDeleteRoutePolicy(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.DeleteRoutePolicy(name, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAddRoutePolicyRule(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	var req newtron.AddRoutePolicyRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	req.Policy = r.PathValue("name")
	opts := execOpts(r)
	_, err := na.do(r.Context(), func() (any, error) {
		return nil, na.net.AddRoutePolicyRule(req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"seq": req.Sequence})
}

func (s *Server) handleRemoveRoutePolicyRule(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	name := r.PathValue("name")
	seq, err := pathInt(r, "seq")
	if err != nil {
		writeError(w, &newtron.ValidationError{Field: "seq", Message: "invalid sequence number"})
		return
	}
	opts := execOpts(r)
	_, err = na.do(r.Context(), func() (any, error) {
		return nil, na.net.RemoveRoutePolicyRule(name, seq, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ============================================================================
// Platform feature support
// ============================================================================

func (s *Server) handlePlatformSupportsFeature(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	platform := r.PathValue("name")
	feature := r.PathValue("feature")
	val, err := na.do(r.Context(), func() (any, error) {
		return map[string]bool{"supported": na.net.PlatformSupportsFeature(platform, feature)}, nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleGetUnsupportedDueTo(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	feature := r.PathValue("name")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.GetUnsupportedDueTo(feature), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

// ============================================================================
// Network provision
// ============================================================================

func (s *Server) handleProvisionDevices(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	var req newtron.ProvisionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.ProvisionDevices(r.Context(), req, opts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleGenerateDeviceComposite(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	device := r.PathValue("device")
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.GenerateDeviceComposite(device)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

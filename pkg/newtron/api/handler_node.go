package api

import (
	"context"
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// ============================================================================
// Node read operations
// ============================================================================

func (s *Server) handleNodeInfo(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.DeviceInfo()
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// handleListInterfaces returns the platform-supported interface inventory for a
// node — every interface the node's platform declares, annotated with topology
// wiring (used/peer) and authored port config. It is a spec-level read
// (NodeInterfaceInventory), so it serves hosts (which have no SONiC device) and
// answers offline, before deployment. Live per-interface state is at
// GET /nodes/{node}/interfaces/{name}.
func (s *Server) handleListInterfaces(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	val, err := ne.net.NodeInterfaceInventory(r.PathValue("node"))
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowInterface(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	name := interfaceName(r)
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ShowInterfaceDetail(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowServiceBinding(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	name := interfaceName(r)
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.GetServiceBindingDetail(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleListVLANs(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.VLANStatus()
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowVLAN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, &newtron.ValidationError{Field: "id", Message: "invalid VLAN ID"})
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ShowVLAN(id)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleListVRFs(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.VRFStatus()
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowVRF(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	name := r.PathValue("name")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ShowVRF(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleListACLs(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ListACLs()
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowACL(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	name := r.PathValue("name")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ShowACL(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleBGPStatus(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.BGPStatus()
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleEVPNStatus(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.EVPNStatus()
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.HealthCheck(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleListLAGs(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.LAGStatus()
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleGetRoute(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	vrf := r.PathValue("vrf")
	prefix := r.PathValue("prefix")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.GetRoute(r.Context(), vrf, prefix)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleGetRouteASIC(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	prefix := r.PathValue("prefix")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.GetRouteASIC(r.Context(), "", prefix)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// ============================================================================
// Node write operations
// ============================================================================

func (s *Server) handleNodeBindMACVPN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req NodeBindMACVPNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.VlanID == 0 || req.MACVPN == "" {
		writeError(w, &newtron.ValidationError{Message: "vlan_id and macvpn are required"})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.BindMACVPN(ctx, req.VlanID, req.MACVPN)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleNodeUnbindMACVPN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req NodeUnbindMACVPNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.VlanID == 0 {
		writeError(w, &newtron.ValidationError{Message: "vlan_id is required"})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.UnbindMACVPN(ctx, req.VlanID)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleReloadConfig(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return nil, n.ConfigReload(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return nil, n.Save(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleSSHCommand(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req SSHCommandRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.Command == "" {
		writeError(w, &newtron.ValidationError{Field: "command", Message: "required"})
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		output, err := n.ExecCommand(r.Context(), req.Command)
		if err != nil {
			return nil, err
		}
		return SSHCommandResponse{Output: output}, nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleCreateVLAN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req VLANCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.CreateVLAN(ctx, req.ID, req.Config())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, val)
}

func (s *Server) handleDeleteVLAN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req struct {
		ID int `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.DeleteVLAN(ctx, req.ID)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleConfigureIRB(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req IRBConfigureRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.ConfigureIRB(ctx, req.VlanID, req.Config())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleCreateVRF(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req VRFCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.CreateVRF(ctx, req.Name, req.Config())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, val)
}

func (s *Server) handleDeleteVRF(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
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
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.DeleteVRF(ctx, req.Name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleCreateACL(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req ACLCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.CreateACL(ctx, req.Name, req.Config())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, val)
}

func (s *Server) handleDeleteACL(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
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
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.DeleteACL(ctx, req.Name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleAddACLRule(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req newtron.ACLRuleAddRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.AddACLRule(ctx, req.ACL, req.RuleName, req.Config())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, val)
}

func (s *Server) handleUpdateACLRule(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req newtron.ACLRuleUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.UpdateACLRule(ctx, req.ACL, req.RuleName, req.Config())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleRemoveACLRule(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req struct {
		ACL  string `json:"acl"`
		Rule string `json:"rule"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.RemoveACLRule(ctx, req.ACL, req.Rule)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleCreatePortChannel(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req PortChannelCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.CreatePortChannel(ctx, req.Name, req.Config())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, val)
}

func (s *Server) handleDeletePortChannel(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
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
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.DeletePortChannel(ctx, req.Name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleAddPortChannelMember(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req PortChannelMemberRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.AddPortChannelMember(ctx, req.PortChannel, req.Interface)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, val)
}

func (s *Server) handleRemovePortChannelMember(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req PortChannelMemberRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.RemovePortChannelMember(ctx, req.PortChannel, req.Interface)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleUnconfigureIRB(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req UnconfigureIRBRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.UnconfigureIRB(ctx, req.VlanID)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// ============================================================================
// VRF IP-VPN binding operations
// ============================================================================

func (s *Server) handleBindIPVPN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req BindIPVPNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.BindIPVPN(ctx, req.IPVPN)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleUnbindIPVPN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req struct {
		IPVPN string `json:"ipvpn"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.UnbindIPVPN(ctx, req.IPVPN)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// ============================================================================
// BGP and static route operations
// ============================================================================

func (s *Server) handleAddBGPEVPNPeer(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req newtron.BGPNeighborConfig
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.AddBGPEVPNPeer(ctx, req)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, val)
}

func (s *Server) handleUpdateBGPEVPNPeer(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	// Decode into the canonical config struct (§25 single owner) — a
	// hand-built request struct here silently dropped the evpn flag: the
	// regenerated entry set lost the BGP_NEIGHBOR_AF row, cs.Replace
	// delivered that as a row DELETE, and frrcfgd faithfully deactivated
	// the l2vpn AF — killing the session the in-place update exists to
	// preserve (RCA-049, redis-MONITOR evidence).
	var req newtron.BGPNeighborConfig
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.UpdateBGPEVPNPeer(ctx, req.NeighborIP, req)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleRemoveBGPEVPNPeer(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var body struct {
		NeighborIP string `json:"neighbor_ip"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.RemoveBGPEVPNPeer(ctx, body.NeighborIP)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleAddStaticRoute(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req StaticRouteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.AddStaticRoute(ctx, req.VRF, req.Prefix, req.NextHop, req.Metric)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, val)
}

func (s *Server) handleUpdateStaticRoute(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req struct {
		VRF     string `json:"vrf"`
		Prefix  string `json:"prefix"`
		NextHop string `json:"nexthop"`
		Metric  int    `json:"metric"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.UpdateStaticRoute(ctx, req.VRF, req.Prefix, req.NextHop, req.Metric)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleRemoveStaticRoute(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req struct {
		VRF    string `json:"vrf"`
		Prefix string `json:"prefix"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.RemoveStaticRoute(ctx, req.VRF, req.Prefix)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// ============================================================================
// Device management operations
// ============================================================================

func (s *Server) handleRestartDaemon(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req RestartDaemonRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.Daemon == "" {
		writeError(w, &newtron.ValidationError{Field: "daemon", Message: "required"})
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return nil, n.RestartService(r.Context(), req.Daemon)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// handleRefreshBGP forces a BGP soft clear on the device to re-advertise routes
// — an operational nudge with no request body (see Node.RefreshBGP).
func (s *Server) handleRefreshBGP(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return nil, n.RefreshBGP(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleSetupDevice(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req newtron.SetupDeviceOpts
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.SetupDevice(ctx, req)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, val)
}

// Diagnostics — ConfigDB / StateDB queries
// ============================================================================

// handleConfigDBSnapshot returns the device's actual CONFIG_DB state as a
// single internally-consistent snapshot (`sonic.RawConfigDB`). Default is
// newtron-owned tables only; ?owned_only=false expands to every schema-known
// table. §46: canonical device-reality substrate exposed directly.
func (s *Server) handleConfigDBSnapshot(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	// Default is the FULL device CONFIG_DB — the endpoint is named configdb
	// and must return it (a curated default misled its first consumer twice;
	// the drift guard's exclusion list is a drift concern, not a read scope).
	// ?owned_only=true opts into the newtron-managed subset.
	ownedOnly := r.URL.Query().Get("owned_only") == "true"
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ConfigDBSnapshot(r.Context(), ownedOnly)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleQueryConfigDB(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	table := r.PathValue("table")
	key := r.PathValue("key")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.QueryConfigDB(table, key)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleConfigDBTableKeys(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	table := r.PathValue("table")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ConfigDBTableKeys(table)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleConfigDBEntryExists(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	table := r.PathValue("table")
	key := r.PathValue("key")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		exists, err := n.ConfigDBEntryExists(table, key)
		if err != nil {
			return nil, err
		}
		return map[string]bool{"exists": exists}, nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleQueryStateDB(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	table := r.PathValue("table")
	key := r.PathValue("key")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.QueryStateDB(table, key)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// ============================================================================
// Status operations
// ============================================================================

func (s *Server) handleCheckBGPSessions(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.CheckBGPSessions(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleShowLAGDetail(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	name := r.PathValue("name")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ShowLAGDetail(name)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// ============================================================================
// Intent operations — Projection, Tree, Drift, Reconcile, Save, Reload, Clear
// ============================================================================

// handleProjection returns the per-table per-key per-field expected state
// derived from intent replay — what newtron believes this device should look
// like. §46: canonical Projection substrate exposed directly.
func (s *Server) handleProjection(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.Projection(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// handleProjectionDiff returns the projection delta that the supplied
// operations would produce on top of the Node's current intent DB. Operations
// are applied in-memory only; the Node's observable state is restored before
// the handler returns. Workbench's pre-commit diff substrate per §46 and
// operator-philosophy invariant #4 (show before do).
func (s *Server) handleProjectionDiff(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req ProjectionDiffRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	val, err := nodeActor.execute(r.Context(), func() (any, error) {
		return nodeActor.node.ProjectionDiff(r.Context(), req.Operations)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.Tree(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleDrift(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.Drift(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

// handleTopologyDrift answers "does the device CONFIG_DB diverge from
// topology.json?" with a freshly-built TopologyNode projection. Distinct
// from handleDrift (issue #75B): that one drifts against the operator's
// current in-memory edits; this one drifts against the on-disk topology.
// Inevitably more expensive — opens a fresh SSH session per call — so it's
// invoked on-demand, not for badge polling.
func (s *Server) handleTopologyDrift(w http.ResponseWriter, r *http.Request) {
	na, _ := s.requireNodeActor(w, r)
	if na == nil {
		return
	}
	device := r.PathValue("node")
	entries, err := na.net.TopologyDrift(r.Context(), device)
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, entries)
}

// handleNodeStatus produces the cheap operator-facing badge data for one
// device (issue #75A). Online + reason come from a non-blocking TCP probe
// against the SSH port; intent_source / has_unsaved_intents come from the
// cached NodeActor state. Drift counts are populated opportunistically when
// the cached actor already has a live device connection; otherwise the
// *_reason field explains why the count is 0.
//
// "Cheap" here means "no new SSH session" — sub-second on a happy path. For
// the full drift answer, callers use /intent/drift and /intent/topology-drift.
func (s *Server) handleNodeStatus(w http.ResponseWriter, r *http.Request) {
	na, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	device := r.PathValue("node")

	status := NodeStatus{
		IntentSource: IntentSourceUnloaded,
	}

	online, reason := na.net.ProbeOnline(r.Context(), device)
	status.Online = online
	status.OnlineReason = reason

	// Read cached actor state under the actor's mutex so a concurrent
	// mutation doesn't tear a Node pointer mid-read. The closure runs on
	// the actor goroutine; nodeActor.node is owned by that goroutine.
	val, doErr := nodeActor.do(r.Context(), func() (any, error) {
		fillNodeStatusFromActor(&status, nodeActor, r.Context())
		return nil, nil
	})
	_ = val
	if doErr != nil {
		writeError(w, doErr)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, status)
}

// fillNodeStatusFromActor reads the cached actor's node (if any) and fills
// the actor-derived fields of status: intent_source, has_unsaved_intents,
// and the opportunistic intent_drift_count. Must be called on the actor
// goroutine so nodeActor.node access is race-free.
//
// Intent drift is computed only when the cached node already has transport
// (Ping succeeds) — that's the "no new SSH session" gate. Topology drift is
// NOT computed here (issue #75A audit): it requires a fresh SSH session
// inside the actor lock, which violates the "cheap" contract. Callers who
// need it call GET /intent/topology-drift explicitly.
func fillNodeStatusFromActor(status *NodeStatus, nodeActor *NodeActor, ctx context.Context) {
	n := nodeActor.node
	if n == nil {
		status.IntentDriftReason = "not_connected"
		return
	}
	status.HasUnsavedIntents = n.HasUnsavedIntents()
	if n.HasActuatedIntent() {
		status.IntentSource = IntentSourceIntent
	} else {
		status.IntentSource = IntentSourceTopology
	}

	if err := n.Ping(ctx); err != nil {
		status.IntentDriftReason = "not_connected"
		return
	}

	intentDrift, err := n.Drift(ctx)
	if err != nil {
		status.IntentDriftReason = "drift_query_failed"
		return
	}
	status.IntentDriftCount = len(intentDrift)
}

func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}

	// Parse reconcile mode: ?reconcile=full|delta
	// Default depends on intent source: topology → full, actuated → delta.
	reconcileMode := r.URL.Query().Get("reconcile")
	if reconcileMode == "" {
		if r.URL.Query().Get("mode") == "topology" {
			reconcileMode = "full"
		} else {
			reconcileMode = "delta"
		}
	}

	opts := execOpts(r)
	if !opts.Execute {
		// Dry-run: return drift as preview (same regardless of mode).
		val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
			return n.Drift(r.Context())
		})
		if err != nil {
			writeError(w, err)
			return
		}
		httputil.WriteJSON(w, http.StatusOK, val)
		return
	}
	reconcileOpts := newtron.ReconcileOpts{Mode: reconcileMode}
	val, err := nodeActor.execute(r.Context(), func() (any, error) {
		return nodeActor.node.Reconcile(r.Context(), reconcileOpts)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.execute(r.Context(), func() (any, error) {
		tree := nodeActor.node.Tree()
		if err := nodeActor.saveTopologyNow(); err != nil {
			return nil, err
		}
		return tree, nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	na, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	if modeFromCtx(r.Context()) != ModeTopology {
		writeError(w, &newtron.ValidationError{Message: "reload is only available in topology mode (add ?mode=topology)"})
		return
	}
	device := r.PathValue("node")
	val, err := nodeActor.do(r.Context(), func() (any, error) {
		nodeActor.closeNode()
		node, err := na.net.BuildTopologyNode(device)
		if err != nil {
			return nil, err
		}
		nodeActor.node = node
		return node.Tree(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleClear(w http.ResponseWriter, r *http.Request) {
	na, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	if modeFromCtx(r.Context()) != ModeTopology {
		writeError(w, &newtron.ValidationError{Message: "clear is only available in topology mode (add ?mode=topology)"})
		return
	}
	device := r.PathValue("node")
	val, err := nodeActor.do(r.Context(), func() (any, error) {
		nodeActor.closeNode()
		node, err := na.net.BuildEmptyTopologyNode(device)
		if err != nil {
			return nil, err
		}
		nodeActor.node = node
		return node.Tree(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

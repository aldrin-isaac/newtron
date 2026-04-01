package api

import (
	"context"
	"net/http"

	"github.com/newtron-network/newtron/pkg/newtron"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
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
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleListInterfaces(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ListInterfaceDetails()
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleListNeighbors(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
		return n.CreateVLAN(ctx, req.ID, newtron.VLANConfig{Description: req.Description})
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, val)
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
	writeJSON(w, http.StatusOK, val)
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
		return n.ConfigureIRB(ctx, req.VlanID, newtron.IRBConfig{
			VRF:        req.VRF,
			IPAddress:  req.IPAddress,
			AnycastMAC: req.AnycastMAC,
		})
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
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
		return n.CreateVRF(ctx, req.Name, newtron.VRFConfig{Name: req.Name})
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, val)
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
	writeJSON(w, http.StatusOK, val)
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
		return n.CreateACL(ctx, req.Name, newtron.ACLConfig{
			Type:        req.Type,
			Stage:       req.Stage,
			Ports:       req.Ports,
			Description: req.Description,
		})
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, val)
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
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleAddACLRule(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req struct {
		ACL      string `json:"acl"`
		RuleName string `json:"rule_name"`
		Priority int    `json:"priority"`
		Action   string `json:"action"`
		SrcIP    string `json:"src_ip"`
		DstIP    string `json:"dst_ip"`
		Protocol string `json:"protocol"`
		SrcPort  string `json:"src_port"`
		DstPort  string `json:"dst_port"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.AddACLRule(ctx, req.ACL, req.RuleName, newtron.ACLRuleConfig{
			Priority: req.Priority,
			Action:   req.Action,
			SrcIP:    req.SrcIP,
			DstIP:    req.DstIP,
			Protocol: req.Protocol,
			SrcPort:  req.SrcPort,
			DstPort:  req.DstPort,
		})
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, val)
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
	writeJSON(w, http.StatusOK, val)
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
		return n.CreatePortChannel(ctx, req.Name, newtron.PortChannelConfig{
			Name:     req.Name,
			Members:  req.Members,
			MinLinks: req.MinLinks,
			FastRate: req.FastRate,
			Fallback: req.Fallback,
			MTU:      req.MTU,
		})
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusCreated, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
}

// ============================================================================
// VRF IP-VPN binding operations
// ============================================================================

func (s *Server) handleBindIPVPN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req struct {
		VRF   string `json:"vrf"`
		IPVPN string `json:"ipvpn"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.BindIPVPN(ctx, req.VRF, req.IPVPN)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleUnbindIPVPN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req struct {
		VRF string `json:"vrf"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.UnbindIPVPN(ctx, req.VRF)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusCreated, val)
}

func (s *Server) handleRemoveBGPEVPNPeer(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var body struct {
		IP string `json:"ip"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.RemoveBGPEVPNPeer(ctx, body.IP)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusCreated, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusCreated, val)
}

// Diagnostics — ConfigDB / StateDB queries
// ============================================================================

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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
}


// ============================================================================
// Intent operations — Tree, Drift, Reconcile, Save, Reload, Clear
// ============================================================================

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
	writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
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
		writeJSON(w, http.StatusOK, val)
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
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	na, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	device := r.PathValue("device")
	val, err := nodeActor.execute(r.Context(), func() (any, error) {
		tree := nodeActor.node.Tree()
		steps := make([]spec.TopologyStep, len(tree.Steps))
		for i, s := range tree.Steps {
			steps[i] = spec.TopologyStep{URL: s.URL, Params: s.Params}
		}
		if err := na.net.SaveDeviceIntents(device, steps); err != nil {
			return nil, err
		}
		nodeActor.node.ClearUnsavedIntents()
		return tree, nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
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
	device := r.PathValue("device")
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
	writeJSON(w, http.StatusOK, val)
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
	device := r.PathValue("device")
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
	writeJSON(w, http.StatusOK, val)
}



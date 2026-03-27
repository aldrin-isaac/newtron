package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron"
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

func (s *Server) handleIntentTree(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	kind := r.URL.Query().Get("kind")
	resource := r.URL.Query().Get("resource")
	ancestors := r.URL.Query().Get("ancestors") == "true"
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.IntentTree(kind, resource, ancestors), nil
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

func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req ExecuteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := newtron.ExecOpts{Execute: req.Execute, NoSave: req.NoSave}
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		for _, op := range req.Operations {
			if err := executeOperation(ctx, n, op); err != nil {
				return fmt.Errorf("operation %s: %w", op.Action, err)
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

// executeOperation dispatches a single Operation to the appropriate Node/Interface method.
func executeOperation(ctx context.Context, n *newtron.Node, op Operation) error {
	switch op.Action {
	case "create-vlan":
		id, _ := intFromAny(op.Params["id"])
		desc, _ := op.Params["description"].(string)
		vni, _ := intFromAny(op.Params["vni"])
		return n.CreateVLAN(ctx, id, newtron.VLANConfig{Description: desc, L2VNI: vni})
	case "delete-vlan":
		id, _ := intFromAny(op.Params["id"])
		return n.DeleteVLAN(ctx, id)
	case "configure-irb":
		id, _ := intFromAny(op.Params["vlan_id"])
		return n.ConfigureIRB(ctx, id, newtron.IRBConfig{
			VRF:        strFromAny(op.Params["vrf"]),
			IPAddress:  strFromAny(op.Params["ip_address"]),
			AnycastMAC: strFromAny(op.Params["anycast_mac"]),
		})
	case "create-vrf":
		name := strFromAny(op.Params["name"])
		return n.CreateVRF(ctx, name, newtron.VRFConfig{Name: name})
	case "delete-vrf":
		name := strFromAny(op.Params["name"])
		return n.DeleteVRF(ctx, name)
	case "create-acl":
		name := strFromAny(op.Params["name"])
		return n.CreateACL(ctx, name, newtron.ACLConfig{
			Type:        strFromAny(op.Params["type"]),
			Stage:       strFromAny(op.Params["stage"]),
			Ports:       strFromAny(op.Params["ports"]),
			Description: strFromAny(op.Params["description"]),
		})
	case "delete-acl":
		name := strFromAny(op.Params["name"])
		return n.DeleteACL(ctx, name)
	case "create-portchannel":
		name := strFromAny(op.Params["name"])
		return n.CreatePortChannel(ctx, name, newtron.PortChannelConfig{
			Name:     name,
			Members:  strSliceFromAny(op.Params["members"]),
			MTU:      intOrZero(op.Params["mtu"]),
			MinLinks: intOrZero(op.Params["min_links"]),
			Fallback: boolFromAny(op.Params["fallback"]),
			FastRate: boolFromAny(op.Params["fast_rate"]),
		})
	case "delete-portchannel":
		name := strFromAny(op.Params["name"])
		return n.DeletePortChannel(ctx, name)
	case "apply-service":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		service := strFromAny(op.Params["service"])
		opts := newtron.ApplyServiceOpts{
			IPAddress: strFromAny(op.Params["ip_address"]),
			PeerAS:    intOrZero(op.Params["peer_as"]),
			VLAN:      intOrZero(op.Params["vlan_id"]),
		}
		if rrc := strFromAny(op.Params["route_reflector_client"]); rrc != "" {
			if opts.Params == nil {
				opts.Params = make(map[string]string)
			}
			opts.Params["route_reflector_client"] = rrc
		}
		if nhs := strFromAny(op.Params["next_hop_self"]); nhs != "" {
			if opts.Params == nil {
				opts.Params = make(map[string]string)
			}
			opts.Params["next_hop_self"] = nhs
		}
		return iface.ApplyService(ctx, service, opts)
	case "remove-service":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.RemoveService(ctx)
	case "refresh-service":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.RefreshService(ctx)
	case "unconfigure-interface":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.UnconfigureInterface(ctx)
	case "bind-acl":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.BindACL(ctx, strFromAny(op.Params["acl"]), strFromAny(op.Params["direction"]))
	case "unbind-acl":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.UnbindACL(ctx, strFromAny(op.Params["acl"]))
	case "bind-macvpn":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.BindMACVPN(ctx, strFromAny(op.Params["macvpn"]))
	case "unbind-macvpn":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.UnbindMACVPN(ctx)
	case "set-port-property":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.SetProperty(ctx, strFromAny(op.Params["property"]), strFromAny(op.Params["value"]))
	case "configure-interface":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		vlanID, _ := intFromAny(op.Params["vlan_id"])
		tagged, _ := op.Params["tagged"].(bool)
		return iface.ConfigureInterface(ctx, newtron.InterfaceConfig{
			VRF: strFromAny(op.Params["vrf"]), IP: strFromAny(op.Params["ip"]),
			VLAN: vlanID, Tagged: tagged,
		})
	case "node-bind-macvpn":
		vlanID, _ := intFromAny(op.Params["vlan_id"])
		vni, _ := intFromAny(op.Params["vni"])
		return n.BindMACVPN(ctx, vlanID, vni)
	case "node-unbind-macvpn":
		vlanID, _ := intFromAny(op.Params["vlan_id"])
		return n.UnbindMACVPN(ctx, vlanID)
	case "apply-qos":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.ApplyQoS(ctx, strFromAny(op.Params["policy"]))
	case "remove-qos":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.RemoveQoS(ctx)
	case "add-bgp-peer":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.AddBGPPeer(ctx, newtron.BGPNeighborConfig{
			NeighborIP:  strFromAny(op.Params["neighbor_ip"]),
			RemoteAS:    intOrZero(op.Params["remote_as"]),
			Description: strFromAny(op.Params["description"]),
			Multihop:    intOrZero(op.Params["multihop"]),
		})
	case "remove-bgp-peer":
		iface, err := n.Interface(op.Interface)
		if err != nil {
			return err
		}
		return iface.RemoveBGPPeer(ctx)
	default:
		return fmt.Errorf("unknown action: %s", op.Action)
	}
}

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
	if req.VlanID == 0 || req.VNI == 0 {
		writeError(w, &newtron.ValidationError{Message: "vlan_id and vni are required"})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.BindMACVPN(ctx, req.VlanID, req.VNI)
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

func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req CleanupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		_, err := n.Cleanup(ctx, req.Type)
		return err
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

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	timeoutStr := r.URL.Query().Get("timeout")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		if timeoutStr != "" {
			timeout, err := time.ParseDuration(timeoutStr)
			if err != nil {
				return nil, &newtron.ValidationError{Field: "timeout", Message: "invalid duration: " + err.Error()}
			}
			return nil, n.RefreshWithRetry(r.Context(), timeout)
		}
		return nil, n.Refresh(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

// ============================================================================
// Node-level QoS operations
// ============================================================================

func (s *Server) handleNodeApplyQoS(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req NodeApplyQoSRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.ApplyQoS(ctx, req.Interface, req.Policy)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleNodeRemoveQoS(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req NodeRemoveQoSRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		return n.RemoveQoS(ctx, req.Interface)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

// ============================================================================
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

func (s *Server) handleVerifyCommitted(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.VerifyCommitted(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

// ============================================================================
// Helpers for execute dispatch
// ============================================================================

func strFromAny(v any) string {
	s, _ := v.(string)
	return s
}

func intFromAny(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}

func intOrZero(v any) int {
	n, _ := intFromAny(v)
	return n
}

func boolFromAny(v any) bool {
	b, _ := v.(bool)
	return b
}

func strSliceFromAny(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// ============================================================================
// Zombie operation handlers
// ============================================================================

func (s *Server) handleReadZombie(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ReadZombie(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleRollbackZombie(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndLocked(r.Context(), func(n *newtron.Node) (any, error) {
		n.SetBypassZombieCheck(true)
		defer n.SetBypassZombieCheck(false)
		if !opts.Execute {
			// Dry-run: read intent and show what would be reversed
			intent, err := n.ReadZombie(r.Context())
			if err != nil {
				return nil, err
			}
			return &newtron.WriteResult{
				Preview: newtron.PreviewRollback(intent),
			}, nil
		}
		return n.RollbackZombie(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleClearZombie(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndLocked(r.Context(), func(n *newtron.Node) (any, error) {
		n.SetBypassZombieCheck(true)
		defer n.SetBypassZombieCheck(false)
		return nil, n.ClearZombie(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

// ============================================================================
// Intents
// ============================================================================

func (s *Server) handleListIntents(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.Intents(), nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

// ============================================================================
// History operations
// ============================================================================

func (s *Server) handleReadHistory(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ReadHistory(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleRollbackHistory(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndLocked(r.Context(), func(n *newtron.Node) (any, error) {
		n.SetBypassZombieCheck(true)
		n.SetSkipHistory(true)
		defer n.SetBypassZombieCheck(false)
		defer n.SetSkipHistory(false)
		if !opts.Execute {
			preview, err := n.PreviewRollbackHistory(r.Context())
			if err != nil {
				return nil, err
			}
			return &newtron.WriteResult{Preview: preview}, nil
		}
		return n.RollbackHistory(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

// ============================================================================
// Device settings
// ============================================================================

func (s *Server) handleReadSettings(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return n.ReadSettings(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleWriteSettings(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var settings newtron.DeviceSettings
	if err := decodeJSON(r, &settings); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	_, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return nil, n.WriteSettings(r.Context(), &settings)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &settings)
}

// ============================================================================
// Drift detection
// ============================================================================

func (s *Server) handleDetectDrift(w http.ResponseWriter, r *http.Request) {
	na, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	device := r.PathValue("device")
	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		return na.net.DetectDrift(r.Context(), device)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleNetworkDrift(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	val, err := na.do(r.Context(), func() (any, error) {
		return na.net.NetworkDrift(r.Context())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

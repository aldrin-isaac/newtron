package api

import (
	"context"
	"net/http"

	"github.com/newtron-network/newtron/pkg/newtron"
)

// ============================================================================
// Interface write operations
// ============================================================================

func (s *Server) handleApplyService(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req ApplyServiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.Service == "" {
		writeError(w, &newtron.ValidationError{Field: "service", Message: "required"})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.ApplyService(ctx, req.Service, newtron.ApplyServiceOpts{
			IPAddress: req.IPAddress,
			VLAN:      req.VLAN,
			PeerAS:    req.PeerAS,
			Params:    req.Params,
		})
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleRemoveService(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.RemoveService(ctx)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleRefreshService(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.RefreshService(ctx)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleUnconfigureInterface(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.UnconfigureInterface(ctx)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleBindACL(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req BindACLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.BindACL(ctx, req.ACL, req.Direction)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleUnbindACL(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req UnbindACLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.UnbindACL(ctx, req.ACL)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleBindMACVPN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req BindMACVPNRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.BindMACVPN(ctx, req.MACVPN)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleUnbindMACVPN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.UnbindMACVPN(ctx)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleAddBGPPeer(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req newtron.BGPNeighborConfig
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.AddBGPPeer(ctx, req)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, val)
}

func (s *Server) handleRemoveBGPPeer(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.RemoveBGPPeer(ctx)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleInterfaceSet(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req InterfaceSetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.SetProperty(ctx, req.Property, req.Value)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleApplyInterfaceQoS(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req ApplyQoSRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.ApplyQoS(ctx, req.Policy)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleConfigureInterface(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req ConfigureInterfaceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.ConfigureInterface(ctx, newtron.InterfaceConfig{
			VRF: req.VRF, IP: req.IP, VLAN: req.VLAN, Tagged: req.Tagged,
		})
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleRemoveInterfaceQoS(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.RemoveQoS(ctx)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

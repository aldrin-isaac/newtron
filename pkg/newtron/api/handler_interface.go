package api

import (
	"context"
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
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
	httputil.WriteJSON(w, http.StatusOK, val)
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
	httputil.WriteJSON(w, http.StatusOK, val)
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
	httputil.WriteJSON(w, http.StatusOK, val)
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
	httputil.WriteJSON(w, http.StatusOK, val)
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
	httputil.WriteJSON(w, http.StatusOK, val)
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
	httputil.WriteJSON(w, http.StatusOK, val)
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
	httputil.WriteJSON(w, http.StatusCreated, val)
}

func (s *Server) handleUpdateBGPPeer(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	// Decode into the canonical config struct (§25 single owner) — an
	// anonymous shadow of BGPNeighborConfig silently diverges the moment
	// the canonical struct grows a field; the evpn-peer twin of this
	// handler dropped the evpn flag exactly that way (RCA-049).
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
		return iface.UpdateBGPPeer(ctx, req)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
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
	httputil.WriteJSON(w, http.StatusOK, val)
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
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleClearProperty(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req InterfaceClearRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.Property == "" {
		writeError(w, &newtron.ValidationError{Field: "property", Message: "required"})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.ClearProperty(ctx, req.Property)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleBindQoS(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req BindQoSRequest
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
		return iface.BindQoS(ctx, req.Policy)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleRemoveTrunkVLAN(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	ifName := interfaceName(r)
	var req RemoveTrunkVLANRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.VLAN <= 0 {
		writeError(w, &newtron.ValidationError{Field: "vlan_id", Message: "must be positive"})
		return
	}
	opts := execOpts(r)
	val, err := nodeActor.connectAndExecute(r.Context(), opts, func(ctx context.Context, n *newtron.Node) error {
		iface, err := n.Interface(ifName)
		if err != nil {
			return err
		}
		return iface.RemoveTrunkVLAN(ctx, req.VLAN)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
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
		return iface.ConfigureInterface(ctx, req.Config())
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

func (s *Server) handleUnbindQoS(w http.ResponseWriter, r *http.Request) {
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
		return iface.UnbindQoS(ctx)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, val)
}

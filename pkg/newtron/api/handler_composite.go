package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/newtron-network/newtron/pkg/newtron"
)

// ============================================================================
// Composite operations (UUID-based handle management)
// ============================================================================

func (s *Server) handleCompositeGenerate(w http.ResponseWriter, r *http.Request) {
	na := s.requireNetwork(w, r)
	if na == nil {
		return
	}
	device := r.PathValue("device")
	nodeActor := na.getNodeActor(device)

	// Generate composite via the Network (abstract node, no device connection).
	val, err := na.do(r.Context(), func() (any, error) {
		ci, err := na.net.GenerateDeviceComposite(device)
		if err != nil {
			return nil, err
		}

		// Store composite in the NodeActor with a UUID.
		uuid := generateUUID()
		nodeActor.storeComposite(uuid, ci)

		return CompositeHandleResponse{
			Handle:     uuid,
			DeviceName: ci.DeviceName,
			EntryCount: ci.EntryCount,
			Tables:     ci.Tables,
		}, nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleCompositeVerify(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req CompositeHandleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.Handle == "" {
		writeError(w, &newtron.ValidationError{Field: "handle", Message: "required"})
		return
	}

	val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
		ci, err := nodeActor.getComposite(req.Handle)
		if err != nil {
			return nil, err
		}
		return n.VerifyComposite(r.Context(), ci)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (s *Server) handleCompositeDeliver(w http.ResponseWriter, r *http.Request) {
	_, nodeActor := s.requireNodeActor(w, r)
	if nodeActor == nil {
		return
	}
	var req CompositeHandleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.Handle == "" {
		writeError(w, &newtron.ValidationError{Field: "handle", Message: "required"})
		return
	}

	mode := newtron.CompositeOverwrite
	if req.Mode == "merge" {
		mode = newtron.CompositeMerge
	}

	val, err := nodeActor.connectAndLocked(r.Context(), func(n *newtron.Node) (any, error) {
		ci, err := nodeActor.getComposite(req.Handle)
		if err != nil {
			return nil, err
		}
		result, err := n.DeliverComposite(r.Context(), ci, mode)
		if err != nil {
			return nil, err
		}
		// Remove composite after successful delivery.
		nodeActor.removeComposite(req.Handle)
		return result, nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

// generateUUID creates a random UUID-like hex string.
func generateUUID() string {
	var buf [16]byte
	rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

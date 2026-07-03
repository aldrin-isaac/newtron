package api

import (
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// handleSetSecret writes a value into the network's secret store — the API/UI
// half of the ${secret:KEY} design (auth-design.md §L0), so an operator populates
// the credential a spec field references (e.g. a node's ssh_pass) through the API
// instead of hand-editing secrets.json. The `secret:"true"` schema flag tells a
// UI to render this input masked and submit it here rather than inline.
//
// Write-only: there is no GET that returns a secret's value, and the audit log
// redacts the `value` field. Gated (in Network.SetSecret) by spec.author.
func (s *Server) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.Key == "" {
		writeError(w, &newtron.ValidationError{Field: "key", Message: "required"})
		return
	}
	if req.Value == "" {
		writeError(w, &newtron.ValidationError{Field: "value", Message: "required"})
		return
	}
	if err := ne.net.SetSecret(r.Context(), req.Key, req.Value, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	// Echo the key only — never the value.
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "set", "key": req.Key})
}

// handleDeleteSecret removes a key from the network's secret store (the reverse
// of handleSetSecret). Same spec.author gate.
func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	key := r.PathValue("key")
	if err := ne.net.DeleteSecret(r.Context(), key, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted", "key": key})
}

package api

import (
	"fmt"
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// handleStartNode restarts a stopped node. Synchronous; the underlying
// newtlab.Lab.Start blocks until QEMU is up.
func (s *Server) handleStartNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	node := r.PathValue("node")
	if name == "" || node == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("lab name and node name required"))
		return
	}
	lab, err := s.openLab(r.Context(), name)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err)
		return
	}
	if err := lab.Start(r.Context(), node); err != nil {
		httputil.WriteError(w, statusFromErr(err), fmt.Errorf("start %s/%s: %w", name, node, err))
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"lab":    name,
		"node":   node,
		"status": "started",
	})
}

// handleStopNode stops a running node by SIGTERM-ing its QEMU process.
// Synchronous.
func (s *Server) handleStopNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	node := r.PathValue("node")
	if name == "" || node == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("lab name and node name required"))
		return
	}
	lab, err := s.openLab(r.Context(), name)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err)
		return
	}
	if err := lab.Stop(r.Context(), node); err != nil {
		httputil.WriteError(w, statusFromErr(err), fmt.Errorf("stop %s/%s: %w", name, node, err))
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"lab":    name,
		"node":   node,
		"status": "stopped",
	})
}

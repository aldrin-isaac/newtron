package api

import (
	"fmt"
	"net/http"
)

// handleStartNode restarts a stopped node. Synchronous; the underlying
// newtlab.Lab.Start blocks until QEMU is up.
func (s *Server) handleStartNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	node := r.PathValue("node")
	if name == "" || node == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("topology name and node name required"))
		return
	}
	lab, err := s.openLab(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err := lab.Start(r.Context(), node); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("start %s/%s: %w", name, node, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"topology": name,
		"node":     node,
		"status":   "started",
	})
}

// handleStopNode stops a running node by SIGTERM-ing its QEMU process.
// Synchronous.
func (s *Server) handleStopNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	node := r.PathValue("node")
	if name == "" || node == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("topology name and node name required"))
		return
	}
	lab, err := s.openLab(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err := lab.Stop(r.Context(), node); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("stop %s/%s: %w", name, node, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"topology": name,
		"node":     node,
		"status":   "stopped",
	})
}

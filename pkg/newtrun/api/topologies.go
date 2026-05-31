// Topology-level HTTP handlers. Currently a single GET endpoint —
// topology authoring is out of scope (issue #33 explicitly excluded
// it, and there's no upstream demand from newtcon yet). The file
// exists per §28 (File-Level Feature Cohesion) so a reader looking
// for "where do topology operations live" finds them by filename
// rather than grepping server.go.
package api

import (
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// handleListTopologies returns the topology names discoverable under
// TopologiesBase. v0 implementation: list immediate subdirectories.
func (s *Server) handleListTopologies(w http.ResponseWriter, r *http.Request) {
	names, err := listSubdirs(s.cfg.TopologiesBase)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, TopologiesResponse{Topologies: names})
}

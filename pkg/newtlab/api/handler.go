package api

import (
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/version"
)

// buildHandler wires the mux with middleware. The route table is the
// canonical list of newtlab-server endpoints; the matching reference
// doc lives at docs/newtlab/api.md.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)

	mux.HandleFunc("GET /api/topologies", s.handleListTopologies)
	mux.HandleFunc("GET /api/topologies/{name}/status", s.handleGetStatus)
	mux.HandleFunc("POST /api/topologies/{name}/deploy", s.handleDeploy)
	mux.HandleFunc("POST /api/topologies/{name}/destroy", s.handleDestroy)
	mux.HandleFunc("POST /api/topologies/{name}/provision", s.handleProvision)
	mux.HandleFunc("GET /api/topologies/{name}/events", s.handleEvents)

	mux.HandleFunc("POST /api/topologies/{name}/nodes/{node}/start", s.handleStartNode)
	mux.HandleFunc("POST /api/topologies/{name}/nodes/{node}/stop", s.handleStopNode)

	var handler http.Handler = mux
	handler = withLogger(s.logger)(handler)
	handler = withRequestID(handler)
	handler = withRecovery(s.logger)(handler)
	return handler
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{
		Status:  "ok",
		Version: version.Version,
	})
}

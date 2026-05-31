package api

import (
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/version"
)

// buildHandler wires the route table with middleware from
// pkg/httputil/. The route table is the canonical list of
// newtlab-server endpoints; the matching reference doc lives at
// docs/newtlab/api.md.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /newtlab/v1/health", s.handleHealth)

	mux.HandleFunc("GET /newtlab/v1/topologies", s.handleListTopologies)
	mux.HandleFunc("GET /newtlab/v1/topologies/{name}/status", s.handleGetStatus)
	mux.HandleFunc("POST /newtlab/v1/topologies/{name}/deploy", s.handleDeploy)
	mux.HandleFunc("POST /newtlab/v1/topologies/{name}/destroy", s.handleDestroy)
	mux.HandleFunc("POST /newtlab/v1/topologies/{name}/provision", s.handleProvision)
	mux.HandleFunc("GET /newtlab/v1/topologies/{name}/events", s.handleEvents)

	mux.HandleFunc("POST /newtlab/v1/topologies/{name}/nodes/{node}/start", s.handleStartNode)
	mux.HandleFunc("POST /newtlab/v1/topologies/{name}/nodes/{node}/stop", s.handleStopNode)

	var handler http.Handler = mux
	handler = httputil.Logger(s.logger)(handler)
	handler = httputil.RequestID(handler)
	handler = httputil.Recovery(s.logger)(handler)
	return handler
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, HealthResponse{
		Status:  "ok",
		Version: version.Version,
	})
}

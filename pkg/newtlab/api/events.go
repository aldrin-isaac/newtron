package api

import (
	"fmt"
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// handleEvents subscribes the caller to the per-topology event
// stream. The SSE framing — initial subscribe comment, heartbeat,
// chunked write with flush — lives in httputil.WriteSSEStream. This
// handler does only what is topology-specific: pull the path
// parameter and route into the shared writer.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("topology name required"))
		return
	}
	httputil.WriteSSEStream(w, r, s.broker, name, s.logger)
}

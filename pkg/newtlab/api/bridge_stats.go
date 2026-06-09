package api

import (
	"fmt"
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

// handlePushBridgeStats receives a BridgeStats push from newtlink.
// Path: POST /newtlab/v1/labs/{lab}/bridges/{host}/stats
//
// The body is the canonical newtlab.BridgeStats — newtlink encodes its
// in-process Bridge.Stats() directly, no translation (§46). Empty {host}
// represents the local worker. Returns 204 on success.
//
// Lab existence is NOT validated here: newtlink starts pushing as soon
// as bridges are up, which can race with the operator destroying the
// lab — the destroy handler evicts any leftover entries, so the worst
// case is one orphaned push that lands and is then dropped. Validating
// would require a synchronous newtlab.ListLabs() round-trip on every
// push (default 5s × N workers) for no operator-visible benefit.
func (s *Server) handlePushBridgeStats(w http.ResponseWriter, r *http.Request) {
	lab := r.PathValue("lab")
	host := r.PathValue("host")
	if lab == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("lab name required"))
		return
	}
	// Per the route shape, {host} is required. The "local worker"
	// case is encoded as the literal "local" segment because URL
	// path values can't be empty strings. The server stores it as ""
	// to match BridgeState.Bridges[""] elsewhere in newtlab.
	if host == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("host segment required"))
		return
	}
	storeHost := host
	if host == "local" {
		storeHost = ""
	}

	var stats newtlab.BridgeStats
	if err := httputil.DecodeJSON(r, &stats); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err)
		return
	}

	s.statsStore.Set(lab, storeHost, stats)
	w.WriteHeader(http.StatusNoContent)
}

// handleGetBridgeStats returns every host's latest snapshot for the lab.
// Path: GET /newtlab/v1/labs/{lab}/bridges/stats
//
// Returns an empty array (HTTP 200) when no host has pushed yet — the
// CLI distinguishes "lab not deployed" (404 from /status) from "lab
// deployed but stats not yet pushed" (empty array here).
func (s *Server) handleGetBridgeStats(w http.ResponseWriter, r *http.Request) {
	lab := r.PathValue("lab")
	if lab == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("lab name required"))
		return
	}
	snaps := s.statsStore.Get(lab)
	httputil.WriteJSON(w, http.StatusOK, snaps)
}

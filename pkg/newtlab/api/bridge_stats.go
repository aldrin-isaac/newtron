package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/httputil/sessionkey"
	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

// handlePushBridgeStats receives a BridgeStats push from newtlink.
// Path: POST /newtlab/v1/labs/{lab}/bridges/{host}/stats
//
// The body is the canonical newtlab.BridgeStats — newtlink encodes its
// in-process Bridge.Stats() directly, no translation (§46). Empty {host}
// represents the local worker. Returns 204 on success.
//
// Authentication: this path is exempt from the server's user-facing
// sessionkey/PAM chain (cmd/newt-server) because newtlink holds neither a
// session key nor PAM credentials. Instead newtlink presents the per-lab
// TelemetryToken as a Bearer, which this handler validates against the lab's
// stored token (LabState.TelemetryToken). The state read that validation
// needs also settles lab existence — a push for an unknown or destroyed lab
// has no token to match and is rejected 401, so a destroy/push race resolves
// to a dropped orphan push rather than a stored one.
func (s *Server) handlePushBridgeStats(w http.ResponseWriter, r *http.Request) {
	lab := r.PathValue("lab")
	host := r.PathValue("host")
	if lab == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("lab name required"))
		return
	}
	if !s.telemetryTokenValid(lab, r.Header.Get("Authorization")) {
		httputil.WriteError(w, http.StatusUnauthorized, fmt.Errorf("invalid telemetry token"))
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

// telemetryTokenValid reports whether authHeader carries the Bearer token that
// matches the lab's stored TelemetryToken (resolved via s.tokenFor, which reads
// the persisted token — so this survives a server restart). Rejects an unknown
// lab (lookup error) or a lab with no token configured, and compares in
// constant time. Strict by design: a lab must have been deployed with a token
// for its newtlink to push — there is no unauthenticated fallback.
func (s *Server) telemetryTokenValid(lab, authHeader string) bool {
	token, err := s.tokenFor(lab)
	if err != nil || token == "" {
		return false
	}
	got, _ := sessionkey.BearerToken(authHeader)
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// defaultTelemetryTokenLookup is the production tokenFor: it reads the lab's
// persisted TelemetryToken from state.json. A missing lab (or unreadable state)
// surfaces as an error, which telemetryTokenValid treats as "reject".
func defaultTelemetryTokenLookup(lab string) (string, error) {
	state, err := newtlab.LoadState(lab)
	if err != nil {
		return "", err
	}
	return state.TelemetryToken, nil
}

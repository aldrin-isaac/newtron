package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
)

// writeControl is one network's write-control reservation: a single holder owns
// the right to mutate the network until they relinquish it or another caller
// takes over. Unlike a lease there is no TTL — the hold is explicit and survives
// until an explicit API call changes it. LastActive is refreshed on every write
// the holder makes (no heartbeat needed); it lets a would-be taker judge whether
// the holder is gone before forcing a takeover.
type writeControl struct {
	Holder     string    `json:"holder"`
	Since      time.Time `json:"since"`
	LastActive time.Time `json:"last_active"`
}

// requestControl grants (or renews) the reservation to caller. Free or
// already-held-by-caller → granted/renewed. Held by another → a
// *WriteControlError unless force, in which case caller takes over and the
// prior holder is returned (displaced, for the response + audit trail).
func (ne *networkEntity) requestControl(netID, caller string, force bool) (wc writeControl, priorHolder string, err error) {
	ne.wcMu.Lock()
	defer ne.wcMu.Unlock()
	now := time.Now()
	if ne.writeCtl == nil || ne.writeCtl.Holder == caller {
		if ne.writeCtl == nil {
			ne.writeCtl = &writeControl{Holder: caller, Since: now}
		}
		ne.writeCtl.LastActive = now
		return *ne.writeCtl, "", nil
	}
	if !force {
		return writeControl{}, "", ne.heldError(netID)
	}
	priorHolder = ne.writeCtl.Holder
	ne.writeCtl = &writeControl{Holder: caller, Since: now, LastActive: now}
	return *ne.writeCtl, priorHolder, nil
}

// relinquishControl frees the reservation if caller holds it. Idempotent: a
// no-op when free or held by someone else (double-relinquish is harmless).
func (ne *networkEntity) relinquishControl(caller string) {
	ne.wcMu.Lock()
	defer ne.wcMu.Unlock()
	if ne.writeCtl != nil && ne.writeCtl.Holder == caller {
		ne.writeCtl = nil
	}
}

// controlStatus returns the current reservation, or false when free.
func (ne *networkEntity) controlStatus() (writeControl, bool) {
	ne.wcMu.Lock()
	defer ne.wcMu.Unlock()
	if ne.writeCtl == nil {
		return writeControl{}, false
	}
	return *ne.writeCtl, true
}

// enforceWrite is the gate the middleware calls before an executing mutation:
// nil when caller holds control (LastActive refreshed), a *WriteControlError
// otherwise — including when nobody holds it (default-closed: a write must
// request control first).
func (ne *networkEntity) enforceWrite(netID, caller string) error {
	ne.wcMu.Lock()
	defer ne.wcMu.Unlock()
	if ne.writeCtl != nil && ne.writeCtl.Holder == caller {
		ne.writeCtl.LastActive = time.Now()
		return nil
	}
	return ne.heldError(netID)
}

// heldError builds the *WriteControlError for the current state. Caller holds wcMu.
func (ne *networkEntity) heldError(netID string) *newtron.WriteControlError {
	e := &newtron.WriteControlError{Network: netID}
	if ne.writeCtl != nil {
		e.Holder = ne.writeCtl.Holder
		e.Since = ne.writeCtl.Since
		e.LastActive = ne.writeCtl.LastActive
	}
	return e
}

// ============================================================================
// Enforcement middleware
// ============================================================================

// withWriteControl refuses an executing mutation by a caller who does not hold
// the target network's write-control reservation, when enforcement is enabled
// (--enforce-write-control). It runs before the mux matches a route, so it reads
// the network id from the path rather than PathValue. Reads, dry-runs, the
// reservation endpoints themselves, and reload/unregister are exempt; with no
// caller identity (dev/loopback) it is a no-op.
func (s *Server) withWriteControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.enforceWriteControl || !writeControlApplies(r) {
			next.ServeHTTP(w, r)
			return
		}
		netID := networkIDFromPath(r.URL.Path)
		caller := audit.CallerFromContext(r.Context())
		if netID == "" || caller == nil || caller.Username == "" {
			next.ServeHTTP(w, r)
			return
		}
		ne := s.getNetwork(netID)
		if ne == nil {
			next.ServeHTTP(w, r) // unknown network → let the handler 404
			return
		}
		if err := ne.enforceWrite(netID, caller.Username); err != nil {
			writeError(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeControlApplies reports whether a request is an executing network mutation
// the reservation guards. Exempt: non-mutating methods, dry-runs, the
// reservation endpoints, reload/unregister (operational), and projection-diff
// (a non-persisting preview).
func writeControlApplies(r *http.Request) bool {
	if !isMutation(r.Method) {
		return false
	}
	if r.URL.Query().Get("dry_run") == "true" {
		return false
	}
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/control/request"), strings.HasSuffix(p, "/control/relinquish"):
		return false
	case strings.HasSuffix(p, "/reload"), strings.HasSuffix(p, "/unregister"):
		return false
	case strings.HasSuffix(p, "/projection-diff"):
		return false
	}
	return true
}

// networkIDFromPath extracts {netID} from /newtron/v1/networks/{netID}/...,
// returning "" when the path has no per-network segment (e.g. POST /networks).
func networkIDFromPath(path string) string {
	const prefix = "/newtron/v1/networks/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return "" // ".../networks/{id}" with no trailing segment is not a mutation route
}

// ============================================================================
// Handlers
// ============================================================================

// controlResponse is the wire shape of GET/POST control replies.
type controlResponse struct {
	Holder      string     `json:"holder"`                 // "" when free
	Since       *time.Time `json:"since,omitempty"`        // nil when free
	LastActive  *time.Time `json:"last_active,omitempty"`  // nil when free
	PriorHolder string     `json:"prior_holder,omitempty"` // set on a takeover
}

func controlResponseFrom(wc writeControl, prior string) controlResponse {
	return controlResponse{Holder: wc.Holder, Since: &wc.Since, LastActive: &wc.LastActive, PriorHolder: prior}
}

// handleControlRequest acquires (or renews / takes over) write control.
// Body: {"force": bool}. force gates on control.takeover; plain on control.request.
func (s *Server) handleControlRequest(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	perm := auth.PermControlRequest
	if req.Force {
		perm = auth.PermControlTakeover
	}
	if err := ne.net.Authorize(r.Context(), perm, auth.NewContext()); err != nil {
		writeError(w, err)
		return
	}
	caller := callerName(r)
	if caller == "" {
		writeError(w, &newtron.ValidationError{Message: "write control requires a verified caller identity"})
		return
	}
	wc, prior, err := ne.requestControl(r.PathValue("netID"), caller, req.Force)
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, controlResponseFrom(wc, prior))
}

// handleControlRelinquish frees write control if the caller holds it.
func (s *Server) handleControlRelinquish(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	if err := ne.net.Authorize(r.Context(), auth.PermControlRequest, auth.NewContext()); err != nil {
		writeError(w, err)
		return
	}
	caller := callerName(r)
	if caller == "" {
		writeError(w, &newtron.ValidationError{Message: "write control requires a verified caller identity"})
		return
	}
	ne.relinquishControl(caller)
	httputil.WriteJSON(w, http.StatusOK, controlResponse{})
}

// handleControlStatus reports the current reservation (open read — no gate).
func (s *Server) handleControlStatus(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	wc, held := ne.controlStatus()
	if !held {
		httputil.WriteJSON(w, http.StatusOK, controlResponse{})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, controlResponseFrom(wc, ""))
}

// callerName returns the verified caller username from the request context, or
// "" when none was attached (standalone/loopback dev server).
func callerName(r *http.Request) string {
	if c := audit.CallerFromContext(r.Context()); c != nil {
		return c.Username
	}
	return ""
}

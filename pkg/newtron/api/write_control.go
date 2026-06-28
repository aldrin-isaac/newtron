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

// defaultControlDuration is the reservation window a request grants when no
// duration is set. The window auto-recovers a dead holder (the reservation
// lapses if not extended) without the implicit heartbeat of a lease — the holder
// extends it deliberately by requesting again.
const defaultControlDuration = 30 * time.Minute

// writeControl is one network's write-control reservation: a single holder owns
// the right to mutate the network until the reservation is released, taken over,
// or its window expires. The window is explicit (default 30m, set or extended on
// request) — not an implicit auto-renewing lease. LastActive (refreshed on the
// holder's writes) is informational, for judging staleness before a takeover.
type writeControl struct {
	Holder     string    `json:"holder"`
	Since      time.Time `json:"since"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastActive time.Time `json:"last_active"`
}

// active reports whether the reservation is still within its window at now. An
// expired reservation counts as free — the holder no longer has control.
func (wc *writeControl) active(now time.Time) bool {
	return wc != nil && now.Before(wc.ExpiresAt)
}

// requestControl acquires or extends the reservation for caller, granting a
// window of dur (defaultControlDuration when dur <= 0). Free, expired, or
// already-held-by-caller → granted/extended to now+dur (extending keeps the
// original Since). Held by another within its window → a *WriteControlError
// unless force, in which case caller takes over and the displaced prior holder
// is returned (for the response + audit trail).
func (ne *networkEntity) requestControl(netID, caller string, force bool, dur time.Duration) (wc writeControl, priorHolder string, err error) {
	if dur <= 0 {
		dur = defaultControlDuration
	}
	ne.wcMu.Lock()
	defer ne.wcMu.Unlock()
	now := time.Now()
	held := ne.writeCtl.active(now)
	if !held || ne.writeCtl.Holder == caller {
		since := now
		if held { // extending our own reservation — keep the original acquire time
			since = ne.writeCtl.Since
		}
		ne.writeCtl = &writeControl{Holder: caller, Since: since, ExpiresAt: now.Add(dur), LastActive: now}
		return *ne.writeCtl, "", nil
	}
	if !force {
		return writeControl{}, "", ne.heldError(netID, now)
	}
	priorHolder = ne.writeCtl.Holder
	ne.writeCtl = &writeControl{Holder: caller, Since: now, ExpiresAt: now.Add(dur), LastActive: now}
	return *ne.writeCtl, priorHolder, nil
}

// releaseControl frees the reservation if caller holds it. Idempotent: a no-op
// when free, expired, or held by someone else (double-release is harmless).
func (ne *networkEntity) releaseControl(caller string) {
	ne.wcMu.Lock()
	defer ne.wcMu.Unlock()
	if ne.writeCtl != nil && ne.writeCtl.Holder == caller {
		ne.writeCtl = nil
	}
}

// controlStatus returns the current reservation, or false when free or expired.
func (ne *networkEntity) controlStatus() (writeControl, bool) {
	ne.wcMu.Lock()
	defer ne.wcMu.Unlock()
	if !ne.writeCtl.active(time.Now()) {
		return writeControl{}, false
	}
	return *ne.writeCtl, true
}

// enforceWrite is the gate the middleware calls before an executing mutation:
// nil when caller holds an active reservation (LastActive refreshed), a
// *WriteControlError otherwise — including when nobody holds it or it has expired
// (default-closed: a write must request control first).
func (ne *networkEntity) enforceWrite(netID, caller string) error {
	ne.wcMu.Lock()
	defer ne.wcMu.Unlock()
	now := time.Now()
	if ne.writeCtl.active(now) && ne.writeCtl.Holder == caller {
		ne.writeCtl.LastActive = now
		return nil
	}
	return ne.heldError(netID, now)
}

// heldError builds the *WriteControlError for the current state. An expired or
// absent reservation reports an empty holder (free — request control first).
// Caller holds wcMu.
func (ne *networkEntity) heldError(netID string, now time.Time) *newtron.WriteControlError {
	e := &newtron.WriteControlError{Network: netID}
	if ne.writeCtl.active(now) {
		e.Holder = ne.writeCtl.Holder
		e.Since = ne.writeCtl.Since
		e.ExpiresAt = ne.writeCtl.ExpiresAt
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
	case strings.HasSuffix(p, "/control/request"), strings.HasSuffix(p, "/control/release"):
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
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`   // nil when free
	LastActive  *time.Time `json:"last_active,omitempty"`  // nil when free
	PriorHolder string     `json:"prior_holder,omitempty"` // set on a takeover
}

func controlResponseFrom(wc writeControl, prior string) controlResponse {
	return controlResponse{
		Holder: wc.Holder, Since: &wc.Since, ExpiresAt: &wc.ExpiresAt,
		LastActive: &wc.LastActive, PriorHolder: prior,
	}
}

// handleControlRequest acquires, extends, or takes over write control. Body:
// {"force": bool, "minutes": int}. minutes sets the reservation window
// (default 30 when ≤ 0); requesting again as the holder extends it. force gates
// on control.takeover (superusers bypass — so a superuser can always take over);
// a plain request gates on control.request.
func (s *Server) handleControlRequest(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		Force   bool `json:"force"`
		Minutes int  `json:"minutes"`
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
	wc, prior, err := ne.requestControl(r.PathValue("netID"), caller, req.Force, time.Duration(req.Minutes)*time.Minute)
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, controlResponseFrom(wc, prior))
}

// handleControlRelease frees write control if the caller holds it.
func (s *Server) handleControlRelease(w http.ResponseWriter, r *http.Request) {
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
	ne.releaseControl(caller)
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

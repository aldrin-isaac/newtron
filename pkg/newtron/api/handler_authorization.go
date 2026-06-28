// handler_authorization.go — HTTP handler for the network's
// authorization table (auth-design.md §L3).
//
// One handler, one endpoint, one cohesive object: the
// authorization table is owned by the network (DPN §27 —
// user_groups, permissions, super_users are three fields of one
// NetworkSpecFile authored together, applied together on
// --enforce-authorization + reload, consumed together by
// auth.Checker). It is read out together, mirroring the
// network.json shape (DPN §46), so a "who has what" inspector
// reads byte-for-byte what an operator would see hand-editing the
// spec file.
package api

import (
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
)

// handleGetAuthorization serves
// GET /newtron/v1/networks/{netID}/authorization. Read-only;
// returns the live authorization table — the same one the auth
// checker is enforcing right now in the registered network.
//
// Gated by PermAuthRead under the engage-when-configured pattern
// (auth-design.md §"auth.read"): the gate fires only when the
// loaded grant table has an auth.read entry. Without such an
// entry, the endpoint stays ungated — existing deployments and the
// zero-ceremony quickstart keep working. The field where-dimension
// is stamped with the spec-fields the response carries
// (super_users,user_groups,permissions) so a clause like
// {"field": "!permissions"} can deny callers asking for the
// permissions block. v1 is full-or-nothing: the entire endpoint
// either returns or 403's; partial redaction is filed as a v2
// follow-up.
func (s *Server) handleGetAuthorization(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	authCtx := auth.NewContext().WithField("super_users,user_groups,permissions")
	if err := ne.net.CheckAuthReadGate(r.Context(), authCtx); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.GetAuthorization())
}

// handleAddSuperUser grants a user per-network super-user status (POST
// .../super-users {user}). Gated by the meta-authorization (spec.author scoped
// to super_users); audited as a mutation. Lets an authorized operator manage
// super-users via the API instead of hand-editing network.json.
func (s *Server) handleAddSuperUser(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	var req struct {
		User string `json:"user"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, &newtron.ValidationError{Message: "invalid JSON: " + err.Error()})
		return
	}
	if req.User == "" {
		writeError(w, &newtron.ValidationError{Field: "user", Message: "required"})
		return
	}
	if err := ne.net.AddSuperUser(r.Context(), req.User, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "added", "user": req.User})
}

// handleRemoveSuperUser revokes a user's per-network super-user status (DELETE
// .../super-users/{user}). Same meta-authorization gate as add.
func (s *Server) handleRemoveSuperUser(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	user := r.PathValue("user")
	if err := ne.net.RemoveSuperUser(r.Context(), user, execOpts(r)); err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "removed", "user": user})
}

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
)

// handleGetAuthorization serves
// GET /newtron/v1/networks/{netID}/authorization. Read-only;
// returns the live authorization table — the same one the auth
// checker is enforcing right now in the registered network.
func (s *Server) handleGetAuthorization(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ne.net.GetAuthorization())
}

package newtser

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/version"
)

// handleHealth — GET /newtser/v1/health (newtser's own probe).
//
// Note the path: newtser's meta-routes are prefixed with /newtser/v1/
// just like the services it proxies. Operators get one consistent
// convention; nothing on newtser's port lives outside the
// /<service>/v1/ namespace. The reverse-proxy catch-all dispatches
// requests with any other first segment to the registered backend.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": version.Version,
	})
}

// handleListServices — GET /newtser/v1/services.
func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.registry.List())
}

// handleRegister — POST /newtser/v1/services.
//
// Idempotent: re-registering an existing name overwrites the prior
// entry (this is how a backend restart works — same name, same
// upstream, fresh LastSeen).
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateRegister(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err)
		return
	}
	svc := s.registry.Register(req.Name, req.Version, req.Upstream)
	s.logger.Printf("registered %q → %s", svc.Name, svc.Upstream)
	httputil.WriteJSON(w, http.StatusCreated, svc)
}

// handleHeartbeat — POST /newtser/v1/services/{name}/heartbeat.
//
// Updates LastSeen without rewriting fields. Returns 404 if the
// service is no longer registered (e.g., evicted) so the backend
// knows to call POST /services to re-register.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, errors.New("name required"))
		return
	}
	if !s.registry.Heartbeat(name) {
		httputil.WriteError(w, http.StatusNotFound,
			fmt.Errorf("service %q not registered (re-register with POST /newtser/v1/services)", name))
		return
	}
	httputil.WriteJSON(w, http.StatusOK, s.registry.Get(name))
}

// handleDeregister — DELETE /newtser/v1/services/{name}.
//
// Called by backends on graceful shutdown. 204 No Content on success;
// 404 if the service wasn't registered (idempotent for the caller's
// purposes — the desired state, "not registered," is achieved either
// way).
func (s *Server) handleDeregister(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, errors.New("name required"))
		return
	}
	if !s.registry.Deregister(name) {
		httputil.WriteError(w, http.StatusNotFound,
			fmt.Errorf("service %q not registered", name))
		return
	}
	s.logger.Printf("deregistered %q", name)
	w.WriteHeader(http.StatusNoContent)
}

// validateRegister enforces the constraints registration data must
// satisfy. The service name must be a valid first-path-segment
// (alphanumeric + hyphen, no slashes, no dots).
func validateRegister(req *RegisterRequest) error {
	if req.Name == "" {
		return errors.New("name required")
	}
	for _, c := range req.Name {
		if !isValidNameChar(c) {
			return fmt.Errorf("name %q contains invalid character %q (allowed: a-z 0-9 -)", req.Name, c)
		}
	}
	if req.Upstream == "" {
		return errors.New("upstream required")
	}
	if !strings.HasPrefix(req.Upstream, "http://") && !strings.HasPrefix(req.Upstream, "https://") {
		return fmt.Errorf("upstream %q must start with http:// or https://", req.Upstream)
	}
	// Reserved names — newtser's own routes share the URL space.
	if req.Name == "newtser" {
		return fmt.Errorf("name %q is reserved for newtser's own routes", req.Name)
	}
	return nil
}

func isValidNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
}

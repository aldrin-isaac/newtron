package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

// handleListTopologies returns every lab newtlab knows about — anything
// with a state directory under ~/.newtlab/labs/. Running and stopped
// labs are both included; clients call GET /{name}/status for
// per-node state.
func (s *Server) handleListTopologies(w http.ResponseWriter, r *http.Request) {
	names, err := newtlab.ListLabs()
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("list labs: %w", err))
		return
	}
	items := make([]TopologyListItem, 0, len(names))
	for _, n := range names {
		items = append(items, TopologyListItem{Name: n})
	}
	httputil.WriteJSON(w, http.StatusOK, items)
}

// handleGetStatus returns the canonical LabState for a deployed
// topology. Mirrors `bin/newtlab status <topology>` without the
// rendering layer.
func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("topology name required"))
		return
	}
	lab, err := s.openLab(r.Context(), name)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err)
		return
	}
	state, err := lab.Status()
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("status %s: %w", name, err))
		return
	}
	httputil.WriteJSON(w, http.StatusOK, StatusResponse{LabState: state})
}

// handleDeploy starts an async deploy. Returns 202 Accepted immediately
// with the start timestamp; phase events flow to subscribers of
// /api/topologies/{name}/events, and terminal state lands in
// state.json (visible via GET /status).
//
// Concurrency: one active deploy per topology. The second concurrent
// request returns 409 Conflict.
func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("topology name required"))
		return
	}

	var req DeployRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err)
		return
	}
	// Query-string fallback so the simplest form
	// `POST /api/topologies/{name}/deploy?provision=true` works without
	// a request body — newtcon hits this from a fetch() without body.
	if !req.Provision {
		if v := r.URL.Query().Get("provision"); v != "" {
			req.Provision, _ = strconv.ParseBool(v)
		}
	}
	if !req.Force {
		if v := r.URL.Query().Get("force"); v != "" {
			req.Force, _ = strconv.ParseBool(v)
		}
	}

	lab, err := s.openLab(r.Context(), name)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err)
		return
	}
	lab.Force = req.Force
	if req.Host != "" {
		lab.FilterHost(req.Host)
	}

	// Acquire the per-topology slot. context.Background() because the
	// deploy outlives this HTTP request.
	deployCtx, release, err := s.registry.Acquire(context.Background(), name)
	if err != nil {
		var already *AlreadyDeployingError
		if errors.As(err, &already) {
			httputil.WriteError(w, http.StatusConflict, already)
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	// Wire newtlab progress callback to the SSE broker.
	lab.OnProgress = func(phase, detail string) {
		s.broker.Publish(name, Event{
			Type:    EventPhase,
			Payload: PhasePayload{Phase: phase, Detail: detail},
		})
	}

	started := time.Now()

	go func() {
		defer release()
		if err := lab.Deploy(deployCtx); err != nil {
			s.broker.Publish(name, Event{
				Type:    EventError,
				Payload: ErrorPayload{Message: err.Error()},
			})
			s.logger.Printf("deploy %s: %v", name, err)
			return
		}
		if req.Provision {
			parallel := req.Parallel
			if parallel <= 0 {
				parallel = 1
			}
			if err := lab.Provision(deployCtx, parallel); err != nil {
				s.broker.Publish(name, Event{
					Type:    EventError,
					Payload: ErrorPayload{Message: fmt.Sprintf("provision: %s", err)},
				})
				s.logger.Printf("provision %s: %v", name, err)
				return
			}
		}
		s.broker.Publish(name, Event{Type: EventComplete, Payload: nil})
	}()

	httputil.WriteJSON(w, http.StatusAccepted, DeployResponse{
		Topology: name,
		Started:  started.Format(time.RFC3339),
	})
}

// handleDestroy tears down a deployed lab. Synchronous — destroy
// typically completes in seconds to tens of seconds and the operator
// expects to block on the response.
func (s *Server) handleDestroy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("topology name required"))
		return
	}
	lab, err := s.openLab(r.Context(), name)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err)
		return
	}
	if err := lab.Destroy(r.Context()); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("destroy %s: %w", name, err))
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"topology": name, "status": "destroyed"})
}

// handleProvision runs newtlab's post-deploy provisioning pass on an
// already-deployed lab. Synchronous; operators that want to deploy +
// provision atomically should use POST /deploy?provision=true.
func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("topology name required"))
		return
	}
	parallel := 1
	if v := r.URL.Query().Get("parallel"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			parallel = n
		}
	}
	lab, err := s.openLab(r.Context(), name)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err)
		return
	}
	if err := lab.Provision(r.Context(), parallel); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("provision %s: %w", name, err))
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"topology": name, "status": "provisioned"})
}

// openLab resolves a topology name to a *newtlab.Lab. Spec data is
// consumed from newtron-server via the configured NewtronClient
// (§27 — newtron owns spec files).
func (s *Server) openLab(ctx context.Context, name string) (*newtlab.Lab, error) {
	if s.cfg.NewtronClient == nil {
		return nil, fmt.Errorf("newtlab-server has no newtron client configured; pass --newtron-server when starting")
	}
	lab, err := newtlab.NewLab(ctx, s.cfg.NewtronClient, name)
	if err != nil {
		return nil, fmt.Errorf("topology %q: %w", name, err)
	}
	return lab, nil
}

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

// handleListLabs returns every lab newtlab knows about — anything with
// a state directory under ~/.newtlab/labs/. Running and stopped labs
// are both included; clients call GET /{name}/status for per-node state.
func (s *Server) handleListLabs(w http.ResponseWriter, r *http.Request) {
	names, err := newtlab.ListLabs()
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("list labs: %w", err))
		return
	}
	items := make([]LabListItem, 0, len(names))
	for _, n := range names {
		items = append(items, LabListItem{NetworkID: n})
	}
	httputil.WriteJSON(w, http.StatusOK, items)
}

// handleGetStatus returns the canonical LabState for a deployed lab.
// Mirrors `bin/newtlab status <lab>` without the rendering layer.
func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("lab name required"))
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
// /api/labs/{name}/events, and terminal state lands in state.json
// (visible via GET /status).
//
// Concurrency: one long-running operation (deploy or provision) per lab —
// runLabOp holds the slot, so a concurrent deploy or provision returns 409.
func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("lab name required"))
		return
	}

	var req DeployRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err)
		return
	}
	// Query-string fallback so the simplest form
	// `POST /api/labs/{name}/deploy?provision=true` works without a
	// request body — newtcon hits this from a fetch() without body.
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
	lab.OrchestratorURL = s.cfg.OrchestratorURL
	if req.Host != "" {
		lab.FilterHost(req.Host)
	}
	parallel := req.Parallel
	if parallel <= 0 {
		parallel = 1
	}

	s.runLabOp(w, r, lab, "deploy", func(ctx context.Context) error {
		if err := lab.Deploy(ctx); err != nil {
			return err
		}
		if req.Provision {
			if err := lab.Provision(ctx, parallel); err != nil {
				return fmt.Errorf("provision: %w", err)
			}
		}
		return nil
	})
}

// runLabOp is the one mechanism every long-running, progress-streaming lab
// operation uses (deploy, provision) — the async counterpart to the short
// synchronous handlers. It acquires the lab's operation slot (409 LabBusyError
// if another op is already in flight, from any caller), wires the lab's
// OnProgress to the per-lab SSE broker, runs fn in a goroutine that outlives
// this request (context.Background), and publishes a terminal complete/error
// event. Returns 202 Accepted; subscribers of GET .../labs/{networkID}/events
// see phase events plus the terminal event. Extracting this keeps deploy and
// provision on one code path rather than duplicating the scaffolding (§27, §28).
func (s *Server) runLabOp(w http.ResponseWriter, r *http.Request, lab *newtlab.Lab, op string, fn func(context.Context) error) {
	name := lab.NetworkID
	// context.Background() because the operation outlives this HTTP request.
	opCtx, release, err := s.registry.Acquire(context.Background(), name, op)
	if err != nil {
		var busy *LabBusyError
		if errors.As(err, &busy) {
			httputil.WriteError(w, http.StatusConflict, busy)
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
		if err := fn(opCtx); err != nil {
			s.broker.Publish(name, Event{
				Type:    EventError,
				Payload: ErrorPayload{Message: err.Error()},
			})
			s.logger.Printf("%s %s: %v", op, name, err)
			return
		}
		s.broker.Publish(name, Event{Type: EventComplete, Payload: nil})
	}()

	httputil.WriteJSON(w, http.StatusAccepted, LabOpResponse{
		Op:        op,
		NetworkID: name,
		Started:   started.Format(time.RFC3339),
	})
}

// handleDestroy tears down a deployed lab. Synchronous — destroy
// typically completes in seconds to tens of seconds and the operator
// expects to block on the response.
func (s *Server) handleDestroy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("lab name required"))
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
	// Evict any leftover bridge-stats snapshots so a redeployed lab
	// doesn't see stale stats from the previous incarnation.
	s.statsStore.EvictLab(name)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"lab": name, "status": "destroyed"})
}

// handleResync re-establishes link telemetry for an already-running lab: it
// ensures a per-lab telemetry token, injects it into the worker's bridge.json,
// and restarts newtlink so it pushes authenticated — without touching the VMs.
// This recovers a lab deployed before the token feature (whose newtlink 401s)
// and is the "resync to a running lab, including newtlink" operation.
func (s *Server) handleResync(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("lab name required"))
		return
	}
	if _, err := newtlab.ResyncBridges(name); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("resync %s: %w", name, err))
		return
	}
	// Drop stale snapshots from the previous newtlink so the first read after
	// resync reflects the restarted worker.
	s.statsStore.EvictLab(name)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"lab": name, "status": "resynced"})
}

// handleProvision runs newtlab's post-deploy provisioning pass on an
// already-deployed lab. Asynchronous like deploy (#373): returns 202 and
// streams per-device progress to /events with a terminal complete/error;
// takes the lab's operation slot, so it 409s if a deploy or another provision
// is already in flight. Operators wanting deploy + provision in one shot use
// POST /deploy?provision=true.
func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("lab name required"))
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
	s.runLabOp(w, r, lab, "provision", func(ctx context.Context) error {
		return lab.Provision(ctx, parallel)
	})
}

// openLab resolves a lab name to a *newtlab.Lab. Spec data is consumed
// from newtron-server via a per-lab newtron client (§27 — newtron owns
// spec files; #116 — each lab has its own network registration slot
// under its name).
func (s *Server) openLab(ctx context.Context, name string) (*newtlab.Lab, error) {
	if s.cfg.NewtronClientFor == nil {
		return nil, fmt.Errorf("newtlab-server has no newtron client configured; pass --newtron-server when starting")
	}
	// The lab's identity is the newtron network it realizes (#396): the {name}
	// path segment IS the network-id, so the client binds to it directly. There
	// is no lab-name-vs-network-id divergence to reconcile.
	client := s.cfg.NewtronClientFor(name)
	lab, err := newtlab.NewLab(ctx, client, name)
	if err != nil {
		return nil, fmt.Errorf("lab %q: %w", name, err)
	}
	return lab, nil
}

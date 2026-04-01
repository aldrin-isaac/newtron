package api

import (
	"context"
	"net/http"
)

// Mode selects how the NodeActor constructs the abstract node.
// Topology mode builds from topology.json; intent (actuated) mode
// builds from the device's own NEWTRON_INTENT records.
type Mode string

const (
	// ModeTopology constructs the node from topology.json steps.
	// The topology is authoritative — the device should match it.
	ModeTopology Mode = "topology"

	// ModeIntent constructs the node from the device's actuated
	// NEWTRON_INTENT records. The device intents are authoritative.
	ModeIntent Mode = "intent"
)

// ctxKey is a private type to avoid collisions in context values.
type ctxKey string

const modeKey ctxKey = "mode"

// withMode injects the request mode into context from the ?mode=topology
// query parameter. Default is ModeIntent (actuated mode).
func withMode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := ModeIntent
		if r.URL.Query().Get("mode") == "topology" {
			mode = ModeTopology
		}
		ctx := context.WithValue(r.Context(), modeKey, mode)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// modeFromCtx reads the mode from context. Returns ModeIntent if not set.
func modeFromCtx(ctx context.Context) Mode {
	if m, ok := ctx.Value(modeKey).(Mode); ok {
		return m
	}
	return ModeIntent
}

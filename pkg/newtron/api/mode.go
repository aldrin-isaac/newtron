package api

import (
	"context"
	"net/http"
)

// Mode selects how the NodeActor constructs the abstract node.
// Topology mode builds from topology.json; intent (actuated) mode
// builds from the device's own NEWTRON_INTENT records; loopback mode
// builds from topology.json on first access then reuses the node,
// letting CLI mutations accumulate for offline config testing.
type Mode string

const (
	// ModeTopology constructs the node from topology.json steps.
	// The topology is authoritative — the device should match it.
	ModeTopology Mode = "topology"

	// ModeIntent constructs the node from the device's actuated
	// NEWTRON_INTENT records. The device intents are authoritative.
	ModeIntent Mode = "intent"

	// ModeLoopback constructs the node from topology.json on first
	// access, then reuses it across requests. Operations accumulate
	// intents in memory without device delivery — the projection is
	// the only output. Used for offline config testing via the CLI.
	ModeLoopback Mode = "loopback"
)

// ctxKey is a private type to avoid collisions in context values.
type ctxKey string

const modeKey ctxKey = "mode"

// withMode injects the request mode into context from the ?mode= query
// parameter. Default is ModeIntent (actuated mode).
func withMode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := ModeIntent
		switch r.URL.Query().Get("mode") {
		case "topology":
			mode = ModeTopology
		case "loopback":
			mode = ModeLoopback
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

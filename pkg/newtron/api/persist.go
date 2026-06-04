package api

import (
	"context"
	"net/http"
)

// PersistMode selects whether a write also persists steps to topology.json.
// Driven by the `?persist=topology` query parameter (issue #75C). The default
// is PersistNone — only set when the operator explicitly opts in. Persisting
// to the device is always done; persisting to topology.json is the optional
// "Apply does both" knob.
type PersistMode string

const (
	// PersistNone — write to the in-memory tree (and the device, when
	// online), but leave topology.json alone. Default.
	PersistNone PersistMode = ""

	// PersistTopology — after a successful write, also rewrite the
	// affected device's entry in topology.json via SaveDeviceIntents.
	// A no-op for read-only handlers (HasUnsavedIntents stays false).
	PersistTopology PersistMode = "topology"
)

const persistKey ctxKey = "persist"

// withPersist injects the parsed `?persist=` query value into context.
// Unrecognized values fall through to PersistNone — the handler treats it
// as the safe default.
func withPersist(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := PersistNone
		if r.URL.Query().Get("persist") == "topology" {
			mode = PersistTopology
		}
		ctx := context.WithValue(r.Context(), persistKey, mode)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// persistFromCtx reads the persist mode from context. Returns PersistNone
// when not set, so callers can ignore the case where withPersist wasn't
// installed (defensive default).
func persistFromCtx(ctx context.Context) PersistMode {
	if m, ok := ctx.Value(persistKey).(PersistMode); ok {
		return m
	}
	return PersistNone
}

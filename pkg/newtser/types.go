// Package newtser is the front-door HTTP server for the newtron-project
// service set. It runs a small reverse proxy + registry: backend
// servers (newtron-server, newtrun-server, newtlab-server, and any
// future newtron-project app) register themselves on startup; newtser
// dispatches incoming HTTP requests to the right backend by the first
// path segment.
//
// The registry is data-driven: newtser knows nothing about the
// backends it routes to beyond their name and upstream URL. Adding a
// new app means writing the app — newtser code does not change.
//
// Registration is HTTP, not a Go interface, so the design works across
// language boundaries: a Python or Node service that follows the same
// register-then-serve protocol is a first-class citizen.
package newtser

import (
	"time"
)

// Service is one registered backend: a name (used as the first path
// segment in newtser-dispatched requests), an upstream URL newtser
// reverse-proxies to, and a LastSeen timestamp the eviction loop uses
// to drop dead registrations.
//
// The version field is informational — clients see the version in the
// URL path itself (e.g., /newtron/v1/...). Storing it here lets
// `GET /services` enumerate what's available without parsing the
// upstream's route table.
type Service struct {
	Name     string    `json:"name"`               // first-segment dispatch key, e.g. "newtron"
	Version  string    `json:"version"`            // human-readable, e.g. "v1"
	Upstream string    `json:"upstream"`           // e.g. "http://127.0.0.1:19080"
	LastSeen time.Time `json:"last_seen"`
}

// RegisterRequest is the body of POST /services.
type RegisterRequest struct {
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	Upstream string `json:"upstream"`
}

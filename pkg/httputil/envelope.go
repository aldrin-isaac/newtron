// Package httputil holds the HTTP server-side primitives shared by every
// newtron-project HTTP server (newtron-server, newtrun-server,
// newtlab-server). The package exists because three near-identical
// copies of envelope / middleware / broker / SSE code accumulated as
// each server was added in turn — see issue #57 for the drift catalog
// and ai-instructions §7 (Second Instance of a Pattern) for the rule
// that says the natural extraction point was the second instance, not
// the fourth.
//
// Consumers import httputil and compose: each server keeps its
// engine-specific Config / Server struct / route table / handlers; the
// generic plumbing (request IDs, panic recovery, JSON envelope, drop-on-
// full event broker, SSE frame writer) lives here once.
package httputil

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// APIResponse is the standard JSON envelope for every newtron-project
// HTTP response. Either `data` (success) or `error` (failure) is set;
// never both. The field set is intentionally minimal — there is no
// "metadata" / "pagination" / "warnings" field — so a generic decoder
// in any language unwraps the same way across all three servers.
type APIResponse struct {
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// WriteJSON writes an APIResponse envelope with status and Data=data.
// Errors from json.Encode are silently dropped — the response has
// already been committed (WriteHeader called) and the client gets a
// truncated body, which is the same behaviour we'd want regardless of
// what the handler did next. Per editing-guidelines §35 (no hedging):
// this is not best-effort, it is correct under the HTTP/1.1 framing
// rules we use (chunked + no Content-Length).
func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIResponse{Data: data})
}

// WriteError writes an APIResponse envelope with status and Error=err.Error().
func WriteError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIResponse{Error: err.Error()})
}

// DecodeJSON decodes the request body into v with unknown-field
// rejection. Empty bodies are allowed — handlers that need a body must
// check the zero value of their decoded struct. Returns a wrapped error
// suitable for passing to WriteError with http.StatusBadRequest.
func DecodeJSON(r *http.Request, v any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("malformed JSON body: %w", err)
	}
	return nil
}

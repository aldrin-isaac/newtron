package api

import (
	"net/http"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/version"
)

// ============================================================================
// Schema metadata endpoints — see docs/newtron/api.md "Schema metadata"
// ============================================================================
//
// These endpoints expose human-facing presentation metadata (label, tooltip,
// enum values, ref-to-other-kind, paths, identifier, validation hints) for
// the spec authoring types. UIs consume them to render forms whose
// vocabulary stays consistent across newtcon, the CLI's HTML preview, and
// any future authoring surface. The metadata is derived at boot from struct
// tags on the spec types themselves (single source of truth — §27).
//
// Operationally these are pure-read endpoints with no network or device
// context — the metadata is global to the newtron install, not per-network.
// They sit at the root of /newtron/v1/, not under /networks/{netID}/.
//
// Three endpoints, each serving a distinct UI use case:
//
//   GET /newtron/v1/schema          summary list (pick-a-type pickers)
//   GET /newtron/v1/schema/all      every full SchemaMeta in one response
//                                   (cold-start panel discovery)
//   GET /newtron/v1/schema/{kind}   one kind's full SchemaMeta (per-form)
//
// All three honor Last-Modified / If-Modified-Since for conditional
// fetches — the schema is baked into the binary at boot, so a single
// timestamp (build time, or process start time as fallback) drives the
// cache contract for every endpoint.

// SchemaList is the response shape of GET /newtron/v1/schema. Surfaces every
// kind currently registered, with its label and description so a UI can
// render a "pick the type to author" picker without fetching each kind
// individually.
type SchemaList struct {
	Kinds []SchemaListEntry `json:"kinds"`
}

// SchemaListEntry summarizes one registered kind for the list endpoint.
type SchemaListEntry struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// SchemaAllResponse is the response shape of GET /newtron/v1/schema/all.
// Surfaces every registered kind's complete SchemaMeta in one round-trip,
// eliminating the N+1 cold-start pattern of GET /schema followed by
// per-kind GET /schema/{kind} fetches.
type SchemaAllResponse struct {
	Schemas []spec.SchemaMeta `json:"schemas"`
}

// schemaLastModified is the canonical Last-Modified time for every
// schema metadata endpoint. The schema is baked into the binary at boot
// (struct tags frozen at compile time), so the binary's identity IS the
// schema's identity. Resolution order:
//
//  1. version.BuildTime (RFC3339) — set by the build pipeline.
//     Stable across process restarts of the same binary.
//  2. Process start time as fallback when BuildTime is empty.
//     Rolling restarts tick the cache; UIs absorb that via their
//     visibility-change re-fetch.
//
// Truncated to second precision because HTTP-date headers carry only
// second resolution.
var schemaLastModified = resolveSchemaLastModified()

func resolveSchemaLastModified() time.Time {
	if version.BuildTime != "" {
		if t, err := time.Parse(time.RFC3339, version.BuildTime); err == nil {
			return t.UTC().Truncate(time.Second)
		}
	}
	return time.Now().UTC().Truncate(time.Second)
}

// writeSchemaCacheHeaders sets Last-Modified on the response and checks
// the request's If-Modified-Since header. Returns true when the client's
// cached copy is fresh enough — caller has already written 304 and must
// return without writing a body.
func writeSchemaCacheHeaders(w http.ResponseWriter, r *http.Request) (notModified bool) {
	w.Header().Set("Last-Modified", schemaLastModified.Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "private, must-revalidate")
	if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if t, err := http.ParseTime(ims); err == nil {
			// HTTP-date format has second resolution; truncate both
			// sides before comparing so the freshness check matches
			// what the client sees on the wire.
			if !schemaLastModified.After(t.UTC().Truncate(time.Second)) {
				w.WriteHeader(http.StatusNotModified)
				return true
			}
		}
	}
	return false
}

func (s *Server) handleSchemaList(w http.ResponseWriter, r *http.Request) {
	if writeSchemaCacheHeaders(w, r) {
		return
	}
	names := spec.ListSchemaKinds()
	entries := make([]SchemaListEntry, 0, len(names))
	for _, name := range names {
		meta := spec.LookupSchema(name)
		if meta == nil {
			continue
		}
		entries = append(entries, SchemaListEntry{
			Kind:        meta.Kind,
			Label:       meta.Label,
			Description: meta.Description,
		})
	}
	httputil.WriteJSON(w, http.StatusOK, SchemaList{Kinds: entries})
}

func (s *Server) handleSchemaAll(w http.ResponseWriter, r *http.Request) {
	if writeSchemaCacheHeaders(w, r) {
		return
	}
	names := spec.ListSchemaKinds()
	schemas := make([]spec.SchemaMeta, 0, len(names))
	for _, name := range names {
		if meta := spec.LookupSchema(name); meta != nil {
			schemas = append(schemas, *meta)
		}
	}
	httputil.WriteJSON(w, http.StatusOK, SchemaAllResponse{Schemas: schemas})
}

func (s *Server) handleSchemaShow(w http.ResponseWriter, r *http.Request) {
	if writeSchemaCacheHeaders(w, r) {
		return
	}
	kind := r.PathValue("kind")
	meta := spec.LookupSchema(kind)
	if meta == nil {
		writeError(w, &newtron.NotFoundError{Resource: "schema", Name: kind})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, meta)
}

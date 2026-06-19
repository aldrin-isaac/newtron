package api

import (
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// ============================================================================
// Schema metadata endpoints — see docs/newtron/api.md "Schema metadata"
// ============================================================================
//
// These endpoints expose human-facing presentation metadata (label, tooltip,
// enum values, ref-to-other-kind) for the spec authoring types. UIs consume
// them to render forms whose vocabulary stays consistent across newtcon, the
// CLI's HTML preview, and any future authoring surface. The metadata is
// derived at boot from struct tags on the spec types themselves (single
// source of truth — §27).
//
// Operationally these are pure-read endpoints with no network or device
// context — the metadata is global to the newtron install, not per-network.
// They sit at the root of /newtron/v1/, not under /networks/{netID}/.

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

func (s *Server) handleSchemaList(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleSchemaShow(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	meta := spec.LookupSchema(kind)
	if meta == nil {
		writeError(w, &newtron.NotFoundError{Resource: "schema", Name: kind})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, meta)
}

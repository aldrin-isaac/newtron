package spec

import (
	"encoding/json"
	"fmt"
)

// PermissionGrant is one entry in a permission's grant list
// (auth-design.md L5). A grant is "these groups, scoped by this
// where clause." A permission can have multiple grants — they're
// evaluated in declaration order; first match wins.
//
// Where is a dimension → pattern map. The dimensions are the same
// names populated on auth.Context: "device", "service", "interface",
// "field". An empty Where matches anything (the legacy behavior —
// equivalent to a pre-L5 ["group1", "group2"] entry).
//
// Pattern syntax — one matcher across all dimensions:
//
//   "edge-1"                       — exact match
//   "edge-*"                       — glob (suffix wildcard)
//   "edge-1,edge-2"                — comma-OR (any of)
//   "!permissions"                 — exclusion (bang prefix)
//   "!permissions,!user_groups"    — exclusion list (none of)
//   "edge-*,!edge-broken"          — mixed: include glob, exclude one
//
// When include and exclude are mixed, the include-set must match AND
// the exclude-set must not match. When everything is excludes, it
// reads as "anything except these" — the shape the meta-authorization
// scenario (spec.author scoped away from the permissions field) uses.
type PermissionGrant struct {
	Groups []string          `json:"groups"`
	Where  map[string]string `json:"where,omitempty"`
}

// PermissionGrants is the typed value of one entry in
// NetworkSpecFile.Permissions or ServiceSpec.Permissions. The custom
// UnmarshalJSON accepts both the new typed form
// ([{"groups": [...], "where": {...}}, ...]) and the legacy
// shorthand (["group1", "group2"]). The shorthand is the only
// compat shim in the auth subsystem — it's load-bearing because it's
// the obvious form for "no scope needed" (auth-design.md §5 L5).
type PermissionGrants []PermissionGrant

// UnmarshalJSON discriminates between the two wire forms by peeking
// at the first array element. Empty arrays decode to nil — equivalent
// to "no grants" — and downstream code treats nil and empty
// identically (no entry matches).
func (g *PermissionGrants) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("permission grants must be a JSON array: %w", err)
	}
	if len(raw) == 0 {
		*g = nil
		return nil
	}

	// Peek at the first element. A JSON string starts with `"`;
	// a JSON object starts with `{`. Whitespace between the array
	// bracket and the first element is consumed by the
	// json.Unmarshal of []json.RawMessage above, so the first byte
	// of raw[0] is the discriminator.
	first := raw[0]
	if len(first) == 0 {
		return fmt.Errorf("permission grant entry is empty")
	}
	switch first[0] {
	case '"':
		// Legacy shorthand: array of group names. Collapse into a
		// single grant with an empty Where — semantically equivalent
		// to the pre-L5 behavior (matches every Context).
		groups := make([]string, len(raw))
		for i, item := range raw {
			if err := json.Unmarshal(item, &groups[i]); err != nil {
				return fmt.Errorf("permission grant shorthand entry %d: %w", i, err)
			}
		}
		*g = []PermissionGrant{{Groups: groups}}
		return nil
	case '{':
		// New typed form: array of {groups, where}.
		grants := make([]PermissionGrant, len(raw))
		for i, item := range raw {
			if err := json.Unmarshal(item, &grants[i]); err != nil {
				return fmt.Errorf("permission grant entry %d: %w", i, err)
			}
		}
		*g = grants
		return nil
	default:
		return fmt.Errorf("permission grant entry must be a string (legacy shorthand) or an object (typed form), got %q", string(first[:1]))
	}
}

// MarshalJSON keeps emit symmetry: typed entries with no Where are
// indistinguishable from the legacy shorthand on the wire, so we
// emit them as bare strings to keep network.json readable. Entries
// with a non-empty Where always emit as objects.
//
// Operators who hand-edit network.json see the form that matches
// their content: a permission with no scoping reads as a flat
// string list (the form they were used to before L5); a scoped
// permission reads as objects with explicit Where dimensions.
func (g PermissionGrants) MarshalJSON() ([]byte, error) {
	if g == nil {
		return []byte("null"), nil
	}
	// All entries have empty Where → emit shorthand for the union
	// of all groups (preserving declaration order).
	allEmpty := true
	for _, grant := range g {
		if len(grant.Where) > 0 {
			allEmpty = false
			break
		}
	}
	if allEmpty {
		// When the slice collapsed to one entry on read, this just
		// emits its Groups as a string list. When the operator
		// authored several no-where grants, we still collapse to one
		// list — the semantic is identical (union of groups).
		groups := make([]string, 0)
		for _, grant := range g {
			groups = append(groups, grant.Groups...)
		}
		return json.Marshal(groups)
	}
	// At least one Where present — emit typed form. Use the typed
	// element type to avoid recursing into PermissionGrants.
	type elem PermissionGrant
	out := make([]elem, len(g))
	for i, grant := range g {
		out[i] = elem(grant)
	}
	return json.Marshal(out)
}

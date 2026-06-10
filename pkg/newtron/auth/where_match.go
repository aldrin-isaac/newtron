package auth

import (
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// whereMatches evaluates a where clause against a populated Context.
//
// An empty or nil where map matches anything — the legacy pre-L5
// behavior (a grant with no scope applies to every Context).
//
// A populated where map matches when EVERY listed dimension's value
// in ctx satisfies that dimension's pattern. The supported dimensions
// are device, service, interface, field — the same names populated on
// auth.Context.
//
// Unknown dimensions FAIL CLOSED — a typo like "devic" in network.json
// produces a denial rather than a silent always-allow. This keeps the
// grant table honest at the cost of one wasted operator question
// ("why won't this match?").
func whereMatches(where map[string]string, ctx *Context) bool {
	if len(where) == 0 {
		return true
	}
	if ctx == nil {
		// Where clause requires a populated dimension but there's
		// nothing to evaluate against — denial.
		return false
	}
	for dim, pattern := range where {
		var value string
		switch dim {
		case "device":
			value = ctx.Device
		case "service":
			value = ctx.Service
		case "interface":
			value = ctx.Interface
		case "resource":
			value = ctx.Resource
		case "field":
			value = ctx.Field
		default:
			return false
		}
		if !matchPattern(pattern, value) {
			return false
		}
	}
	return true
}

// matchPattern implements one matcher used across every where
// dimension. The grammar:
//
//	exact:        "edge-1"          — value == "edge-1"
//	glob:         "edge-*"          — value starts with "edge-"
//	OR list:      "edge-1,edge-2"   — value in {edge-1, edge-2}
//	exclusion:    "!permissions"    — value != "permissions"
//	exclude list: "!a,!b,!c"        — value not in {a, b, c}
//	mixed:        "edge-*,!edge-broken"
//	                                — value matches "edge-*" AND not "edge-broken"
//
// Mixed semantics: at least one include matches AND no exclude
// matches. When the pattern consists only of excludes (the
// meta-authorization scenario), "at least one include" is vacuously
// true — the value matches if no exclude rules it out.
//
// An empty pattern matches an empty value only; this prevents an
// operator from accidentally granting all by leaving a dimension
// blank in network.json.
func matchPattern(pattern, value string) bool {
	includes, excludes := splitPattern(pattern)
	for _, exclude := range excludes {
		if matchSingle(exclude, value) {
			return false
		}
	}
	if len(includes) == 0 && len(excludes) == 0 {
		// An explicitly empty pattern (or one made of only whitespace
		// + commas) matches an empty value only — fail closed against
		// the "operator accidentally wrote nothing" case.
		return value == ""
	}
	if len(includes) == 0 {
		// Exclude-only pattern: matches if no exclude matched.
		return true
	}
	for _, include := range includes {
		if matchSingle(include, value) {
			return true
		}
	}
	return false
}

// splitPattern splits a comma list into include and exclude buckets.
// Bang-prefixed items go to the exclude bucket; the rest to include.
// Leading/trailing whitespace on each item is trimmed.
func splitPattern(pattern string) (includes, excludes []string) {
	for item := range strings.SplitSeq(pattern, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.HasPrefix(item, "!") {
			excludes = append(excludes, item[1:])
		} else {
			includes = append(includes, item)
		}
	}
	return includes, excludes
}

// matchSingle handles one pattern atom: exact or trailing glob.
// Glob support is intentionally limited to a single trailing `*`
// (the common "all devices in this rack" / "all services in this
// VRF" scenario). Embedded or leading globs aren't supported — they
// invite "regex by accident" syntax confusion.
func matchSingle(pattern, value string) bool {
	if strings.HasSuffix(pattern, "*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(value, prefix)
	}
	return pattern == value
}

// grantsMatch reports whether any grant in the slice is satisfied by
// the (username, ctx) pair. First-match wins — declaration order in
// network.json determines evaluation order, mirroring how operators
// read the file top-down.
//
// A grant is satisfied when:
//  1. username is a member of one of grant.Groups (literal username
//     match OR membership in a UserGroups entry of the same name); AND
//  2. ctx satisfies grant.Where (or Where is empty/nil).
func (c *Checker) grantsMatch(username string, grants spec.PermissionGrants, ctx *Context) bool {
	for _, grant := range grants {
		if !c.userInGroups(username, grant.Groups) {
			continue
		}
		if !whereMatches(grant.Where, ctx) {
			continue
		}
		return true
	}
	return false
}

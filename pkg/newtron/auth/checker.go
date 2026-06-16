package auth

import (
	"fmt"
	"slices"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// Checker decides whether a caller is granted a permission against a
// loaded NetworkSpecFile. Caller identity is supplied per-call via
// Context.Caller — the Checker holds no ambient "current user."
type Checker struct {
	network *spec.NetworkSpecFile
}

// NewChecker builds a Checker bound to network. The returned Checker
// is stateless w.r.t. caller identity — every Check reads the username
// from its Context argument.
func NewChecker(network *spec.NetworkSpecFile) *Checker {
	return &Checker{network: network}
}

// Check decides whether ctx.Caller has permission. A nil ctx or an
// empty Caller field is denied unconditionally — the absence of a
// caller IS the absence of a verified identity, which fails closed
// (auth-design.md L3). The guard runs before any super-user or
// grant-list lookup, so a degenerate authorization table (e.g.
// `super_users: [""]`, `user_groups: {g: [""]}`, or a grant
// `["", ...]`) cannot turn an anonymous request into an authorized
// one. The HTTP boundary populates Caller from a verified identity;
// without one, Check fails — that is the structural contract.
func (c *Checker) Check(permission Permission, ctx *Context) error {
	if ctx == nil || ctx.Caller == "" {
		return &PermissionError{
			User:       "",
			Permission: permission,
			Context:    ctx,
		}
	}
	return c.checkUser(ctx.Caller, permission, ctx)
}

// checkUser evaluates one user against the loaded grant table.
// Precondition: username is non-empty (Check enforces this). The
// super-user bypass and the global grant table are consulted in
// that order; first match wins. Falls through to a PermissionError
// when nothing matches.
//
// Per-service scoping is expressed via L5 `where: {service: ...}`
// clauses on global grants (auth-design.md §L5) — the per-service
// override path that used to consult ServiceSpec.Permissions was
// collapsed in #165 because L5 already expressed the same
// constraint uniformly and the embedded mechanism duplicated the
// network's authorization table inside instance specs (DPN §27).
func (c *Checker) checkUser(username string, permission Permission, ctx *Context) error {
	if c.isSuperUser(username) {
		return nil
	}
	if c.checkGlobalPermission(username, permission, ctx) {
		return nil
	}
	return &PermissionError{
		User:       username,
		Permission: permission,
		Context:    ctx,
	}
}

func (c *Checker) isSuperUser(username string) bool {
	return slices.Contains(c.network.SuperUsers, username)
}

// HasPermissionEntry reports whether the loaded grant table has any
// entry (legacy shorthand or typed) for permission. Used by
// engage-when-configured gates that want to fall back to legacy
// allow-all behavior until an operator explicitly opts in by adding
// the first grant entry. PermAuthRead is the load-bearing consumer:
// reading the grant table stays ungated until the operator adds the
// first auth.read grant, at which point the gate engages normally.
//
// Returns false when the network has no permissions map at all (a
// minimal network.json) and when the permission's slice is present
// but empty. Either way the gate's engage-when-configured semantics
// fall back to allow.
func (c *Checker) HasPermissionEntry(permission Permission) bool {
	if c.network == nil || len(c.network.Permissions) == 0 {
		return false
	}
	grants, ok := c.network.Permissions[string(permission)]
	if !ok {
		return false
	}
	return len(grants) > 0
}

func (c *Checker) checkGlobalPermission(username string, permission Permission, ctx *Context) bool {
	return c.checkPermissionMap(username, permission, c.network.Permissions, ctx)
}

// checkPermissionMap walks the permission's grant list, evaluating
// each grant's group membership AND where clause against the caller
// + context (auth-design.md L5). The "all" wildcard is checked first;
// when present, its grants apply to every permission.
//
// First-match wins — declaration order in network.json determines
// evaluation order. Grants with an empty Where clause behave
// identically to the pre-L5 flat group list (matches every Context).
func (c *Checker) checkPermissionMap(username string, permission Permission, permMap map[string]spec.PermissionGrants, ctx *Context) bool {
	if grants, ok := permMap["all"]; ok {
		if c.grantsMatch(username, grants, ctx) {
			return true
		}
	}
	grants, ok := permMap[string(permission)]
	if !ok {
		return false
	}
	return c.grantsMatch(username, grants, ctx)
}

func (c *Checker) userInGroups(username string, allowedGroups []string) bool {
	for _, group := range allowedGroups {
		if group == username {
			return true
		}
		if members, ok := c.network.UserGroups[group]; ok {
			if slices.Contains(members, username) {
				return true
			}
		}
	}
	return false
}

// PermissionError represents a permission denial
type PermissionError struct {
	User       string
	Permission Permission
	Context    *Context
}

func (e *PermissionError) Error() string {
	msg := fmt.Sprintf("permission denied: user '%s' does not have '%s' permission", e.User, e.Permission)
	if e.Context != nil {
		if e.Context.Service != "" {
			msg += fmt.Sprintf(" for service '%s'", e.Context.Service)
		}
		if e.Context.Device != "" {
			msg += fmt.Sprintf(" on device '%s'", e.Context.Device)
		}
	}
	return msg
}

func (e *PermissionError) Unwrap() error {
	return util.ErrPermissionDenied
}

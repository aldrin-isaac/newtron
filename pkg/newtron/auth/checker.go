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

// Check decides whether ctx.Caller has permission. A nil or empty-
// Caller Context is denied: at the L3 boundary, the absence of a
// caller is the absence of a verified identity, which fails closed.
func (c *Checker) Check(permission Permission, ctx *Context) error {
	username := ""
	if ctx != nil {
		username = ctx.Caller
	}
	return c.checkUser(username, permission, ctx)
}

// checkUser verifies if a specific user has a permission
func (c *Checker) checkUser(username string, permission Permission, ctx *Context) error {
	// Superusers can do anything
	if c.isSuperUser(username) {
		return nil
	}

	// Check service-specific permissions first
	if ctx != nil && ctx.Service != "" {
		if svc, ok := c.network.Services[ctx.Service]; ok {
			if allowed := c.checkServicePermission(username, permission, svc, ctx); allowed {
				return nil
			}
		}
	}

	// Check global permissions
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

func (c *Checker) checkServicePermission(username string, permission Permission, svc *spec.ServiceSpec, ctx *Context) bool {
	if svc.Permissions == nil {
		return false
	}
	return c.checkPermissionMap(username, permission, svc.Permissions, ctx)
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

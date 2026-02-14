package auth

import (
	"fmt"
	"os/user"
	"slices"

	"github.com/newtron-network/newtron/pkg/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// Checker validates user permissions
type Checker struct {
	network     *spec.NetworkSpecFile
	currentUser string
}

// NewChecker creates a permission checker
func NewChecker(network *spec.NetworkSpecFile) *Checker {
	username := "unknown"
	if u, err := user.Current(); err == nil {
		username = u.Username
	}

	return &Checker{
		network:     network,
		currentUser: username,
	}
}

// SetUser overrides the current user (for testing or sudo)
func (c *Checker) SetUser(username string) {
	c.currentUser = username
}

// CurrentUser returns the current username
func (c *Checker) CurrentUser() string {
	return c.currentUser
}

// Check verifies if the current user has a permission
func (c *Checker) Check(permission Permission, ctx *Context) error {
	return c.CheckUser(c.currentUser, permission, ctx)
}

// CheckUser verifies if a specific user has a permission
func (c *Checker) CheckUser(username string, permission Permission, ctx *Context) error {
	// Superusers can do anything
	if c.isSuperUser(username) {
		return nil
	}

	// Check service-specific permissions first
	if ctx != nil && ctx.Service != "" {
		if svc, ok := c.network.Services[ctx.Service]; ok {
			if allowed := c.checkServicePermission(username, permission, svc); allowed {
				return nil
			}
		}
	}

	// Check global permissions
	if c.checkGlobalPermission(username, permission) {
		return nil
	}

	return &PermissionError{
		User:       username,
		Permission: permission,
		Context:    ctx,
	}
}

// IsSuperUser returns true if the current user is a superuser
func (c *Checker) IsSuperUser() bool {
	return c.isSuperUser(c.currentUser)
}

func (c *Checker) isSuperUser(username string) bool {
	return slices.Contains(c.network.SuperUsers, username)
}

func (c *Checker) checkServicePermission(username string, permission Permission, svc *spec.ServiceSpec) bool {
	if svc.Permissions == nil {
		return false
	}
	return c.checkPermissionMap(username, permission, svc.Permissions)
}

func (c *Checker) checkGlobalPermission(username string, permission Permission) bool {
	return c.checkPermissionMap(username, permission, c.network.Permissions)
}

// checkPermissionMap checks whether username has the given permission in permMap.
// It first checks the "all" wildcard key, then the specific permission key.
func (c *Checker) checkPermissionMap(username string, permission Permission, permMap map[string][]string) bool {
	// Check for "all" permission first
	if groups, ok := permMap["all"]; ok {
		if c.userInGroups(username, groups) {
			return true
		}
	}

	// Check specific permission
	groups, ok := permMap[string(permission)]
	if !ok {
		return false
	}

	return c.userInGroups(username, groups)
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

package newtron

import (
	"context"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// ssh_credentials_ops.go — the public API for authoring the device SSH login at
// network / zone / node scope. The scalar counterpart of the map-overridable
// spec writes (spec_ops.go): same ScopeSelector surface so a schema-driven UI
// renders one flat form with a scope dropdown, but the login is a singleton per
// scope, so the verbs are set / clear (not create/update/delete-by-name).

// SetSSHCredentials sets the device SSH login at a scope (network / zone / node).
// Gated by spec.author (field "ssh-credentials", resource = the scope instance).
// ssh_pass is stored verbatim — a ${secret:KEY} reference is honored at resolve
// time; a plaintext value is accepted, though the masked (secret:"true") schema
// field steers operators to store it in the secret store and reference it.
func (net *Network) SetSSHCredentials(ctx context.Context, req SetSSHCredentialsRequest, opts ExecOpts) error {
	if err := validateScopeSelector(req.ScopeSelector); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor,
			auth.NewContext().WithField("ssh-credentials").WithResource(req.ScopeInstance)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return translateInternalError(net.internal.SetSSHCredentials(req.Scope, req.ScopeInstance, req.SSHUser, req.SSHPass))
}

// ClearSSHCredentials removes the SSH login override at a scope — the reverse of
// SetSSHCredentials (§15), same gate. Always safe: resolution falls back through
// the hierarchy (node > zone > network > platform > "admin"), so unlike a
// network-base spec delete it needs no consumer/override guard.
func (net *Network) ClearSSHCredentials(ctx context.Context, sel ScopeSelector, opts ExecOpts) error {
	if err := validateScopeSelector(sel); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor,
			auth.NewContext().WithField("ssh-credentials").WithResource(sel.ScopeInstance)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return translateInternalError(net.internal.ClearSSHCredentials(sel.Scope, sel.ScopeInstance))
}

// ShowSSHCredentials reads the login authored at one scope (no hierarchy
// fallback), with ssh_pass masked so no secret value reaches the wire — the read
// mirror of SetSSHCredentials (§24). For the EFFECTIVE login a device dials
// (after the node > zone > network merge), read the resolved node spec via
// GET /nodes/{name}.
func (net *Network) ShowSSHCredentials(sel ScopeSelector) (*SSHCredentialsView, error) {
	if err := validateScopeSelector(sel); err != nil {
		return nil, err
	}
	c, err := net.internal.GetSSHCredentialsAt(sel.Scope, sel.ScopeInstance)
	if err != nil {
		return nil, translateInternalError(err)
	}
	scope := sel.Scope
	if scope == "" {
		scope = spec.ScopeNetwork
	}
	return &SSHCredentialsView{
		Scope:         scope,
		ScopeInstance: sel.ScopeInstance,
		SSHUser:       c.SSHUser,
		SSHPass:       maskSSHPass(c.SSHPass),
	}, nil
}

// maskSSHPass keeps a ${secret:KEY} reference intact — a pointer a UI/auditor
// needs to see which key is referenced, the same rule the audit redactor uses —
// and replaces any plaintext with a fixed placeholder so a stored secret value
// never reaches the wire. Empty stays empty (nothing authored at this scope).
func maskSSHPass(pass string) string {
	if pass == "" || strings.HasPrefix(pass, "${secret:") {
		return pass
	}
	return "***redacted***"
}

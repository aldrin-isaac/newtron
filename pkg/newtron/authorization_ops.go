// authorization_ops.go — public API surface for reading the network's
// authorization table (auth-design.md §L3).
//
// The internal *network.Network owns the authorization-table state
// (DPN §27 single owner — user_groups, permissions, super_users are
// three fields of one NetworkSpecFile authored together, applied
// together on --enforce-authorization + reload, consumed together by
// the auth.Checker). This file is the *newtron.Network → wire-type
// boundary: one accessor that mirrors the network.json shape
// (DPN §46) so an external consumer (newtcon's "who has what"
// inspector, future entitlement-table CLIs, etc.) reads the same
// catalog the runtime checker enforces.
package newtron

import (
	"context"

	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
)

// GetAuthorization returns the network's authorization table as the
// API view AuthorizationDetail. One round trip; no per-permission
// further reads required.
func (net *Network) GetAuthorization() *AuthorizationDetail {
	a := net.internal.GetAuthorization()
	return &AuthorizationDetail{
		UserGroups:  a.UserGroups,
		Permissions: a.Permissions,
		SuperUsers:  a.SuperUsers,
	}
}

// CheckAuthReadGate engages the PermAuthRead gate under the
// engage-when-configured pattern: returns nil when no auth.read
// entry exists in the grant table (preserves the legacy ungated
// behavior the endpoint shipped with), otherwise runs the standard
// gate. Used by the HTTP handler for GET /authorization. authCtx's
// Field should be pre-populated with the spec-fields the response
// carries (super_users,user_groups,permissions) so a where:{field}
// clause can scope.
func (net *Network) CheckAuthReadGate(ctx context.Context, authCtx *auth.Context) error {
	return net.checkPermissionIfConfigured(ctx, auth.PermAuthRead, authCtx)
}

// CheckAuditReadGate engages the PermAuditRead gate under the same
// engage-when-configured pattern as CheckAuthReadGate: returns nil
// when no audit.read entry exists in the grant table (preserves the
// legacy ungated behavior the audit endpoints shipped with),
// otherwise runs the standard gate. Used by the HTTP handlers for
// GET /audit/events and GET /audit/integrity. The handler should
// stamp authCtx.Field with "audit_events" or "audit_integrity" so
// a where:{field: ...} clause can scope to one surface.
func (net *Network) CheckAuditReadGate(ctx context.Context, authCtx *auth.Context) error {
	return net.checkPermissionIfConfigured(ctx, auth.PermAuditRead, authCtx)
}

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

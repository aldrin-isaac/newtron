// secret_ops.go — public API surface for writing the network's secret store
// (auth-design.md §L0). The API/UI half of the ${secret:KEY} design: an operator
// populates the credential a spec field references (e.g. a node's ssh_pass)
// through the API instead of hand-editing secrets.json.
//
// The internal *network.Network owns the secret store (DPN §27). This file is
// the *newtron.Network → auth boundary: SetSecret / DeleteSecret gate on
// spec.author (a secret backs a spec-authored field — the same permission that
// authors the ${secret:KEY} reference) and delegate to the internal store. The
// value is write-only: there is no read-back through the API.
package newtron

import (
	"context"

	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
)

// ListSecrets returns the key names in the network's secret store — never the
// values (Store.List's contract, so a listing can't leak secrets). The read that
// mirrors SetSecret (§24): an operator or UI can see which credentials are set
// (e.g. render a "✓ set" indicator) without exposing them. Gated by the same
// spec.author/secrets permission as the write — the role that sets a credential
// is the role that sees which exist.
func (net *Network) ListSecrets(ctx context.Context) ([]string, error) {
	if err := net.checkPermission(ctx, auth.PermSpecAuthor,
		auth.NewContext().WithField("secrets")); err != nil {
		return nil, err
	}
	return net.internal.ListSecrets()
}

// SetSecret writes key → value in the network's secret store, creating the
// per-network secrets.json on first write. Gated by spec.author, scoped to the
// `secrets` field so a service-architect role scoped `!secrets` cannot inject
// credentials. Audited as a mutation with the value redacted. Idempotent.
func (net *Network) SetSecret(ctx context.Context, key, value string, opts ExecOpts) error {
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor,
			auth.NewContext().WithField("secrets").WithResource(key)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.SetSecret(key, value)
}

// DeleteSecret removes key from the network's secret store — the reverse of
// SetSecret (§15). Same spec.author gate.
func (net *Network) DeleteSecret(ctx context.Context, key string, opts ExecOpts) error {
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor,
			auth.NewContext().WithField("secrets").WithResource(key)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteSecret(key)
}

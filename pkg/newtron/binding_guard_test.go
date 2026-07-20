package newtron

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// loadServiceFixture copies the 2node-vs-service network into a temp dir and
// loads it. The copy isolates the committed fixture from the force-cascade
// path, which persists topology.json. The fixture applies service "rtd" on
// switch1:Ethernet0 and switch2:Ethernet0 (two bindings across two devices) and
// "irb" on Ethernet4 of each — used to prove the cascade is scoped.
func loadServiceFixture(t *testing.T) (*Network, string) {
	t.Helper()
	src := filepath.Join("..", "..", "networks", "2node-vs-service")
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	net, err := LoadNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadNetwork: %v", err)
	}
	return net, dir
}

// TestDeleteService_RefusedWhenApplied — a service still applied on an interface
// (an apply-service topology step references it) must not be deletable. The
// refusal is a *util.ConflictError (→ 409) enumerating every device:interface
// binding, with force_available set. This is the dimension the spec-reference
// and override guards cannot see: apply-service bindings live in topology steps,
// not the spec ref graph.
func TestDeleteService_RefusedWhenApplied(t *testing.T) {
	net, _ := loadServiceFixture(t)
	ctx := context.Background()
	exec := ExecOpts{Execute: true}

	err := net.DeleteService(ctx, ScopeSelector{}, "rtd", exec, false)
	var conflict *util.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("DeleteService without force: err=%v, want *util.ConflictError", err)
	}
	if conflict.Resource != "ServiceSpec" || conflict.Name != "rtd" || !conflict.Force {
		t.Errorf("conflict = %+v, want Resource=ServiceSpec Name=rtd Force=true", conflict)
	}
	want := []string{"switch1:Ethernet0", "switch2:Ethernet0"}
	if !reflect.DeepEqual(conflict.References, want) {
		t.Errorf("references = %v, want %v", conflict.References, want)
	}
	// The refusal must not have deleted the spec.
	if got := net.bindingsFor(SpecKindService, "rtd"); len(got) != 2 {
		t.Errorf("after refusal: bindings=%v, want 2 (nothing removed)", got)
	}
}

// TestDeleteService_ForceCascadesBindings — with force, the guard cascade-
// removes the service's apply-service steps from topology.json (§15 reference-
// aware reverse) and persists, leaving no dangling step. Unrelated services'
// bindings are untouched, and the removal survives a reload from disk.
//
// Exercised at the guardSpecBindings boundary (the force outcome) rather than
// through the full DeleteService: the cascade runs before internal spec
// deletion (remove bindings, then delete spec — never the reverse), and the
// spec-deletion half is covered by the references_integration tests. The
// cascade-first order is what this test pins.
func TestDeleteService_ForceCascadesBindings(t *testing.T) {
	net, dir := loadServiceFixture(t)

	if got := net.bindingsFor(SpecKindService, "rtd"); len(got) != 2 {
		t.Fatalf("precondition: rtd bindings=%v, want 2", got)
	}

	if err := net.guardSpecBindings(spec.ScopeNetwork, SpecKindService, "ServiceSpec", "rtd", true); err != nil {
		t.Fatalf("force cascade: %v", err)
	}

	if got := net.bindingsFor(SpecKindService, "rtd"); len(got) != 0 {
		t.Errorf("after force: rtd bindings=%v, want none", got)
	}
	// The cascade is scoped to the deleted service — irb stays applied.
	if got := net.bindingsFor(SpecKindService, "irb"); len(got) != 2 {
		t.Errorf("force cascade touched an unrelated service: irb=%v, want 2", got)
	}

	// Persisted: a fresh load from disk sees the rtd steps gone and irb intact.
	reload, err := LoadNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reload.bindingsFor(SpecKindService, "rtd"); len(got) != 0 {
		t.Errorf("after reload: rtd bindings=%v, want none (cascade not persisted)", got)
	}
	if got := reload.bindingsFor(SpecKindService, "irb"); len(got) != 2 {
		t.Errorf("after reload: irb bindings=%v, want 2", got)
	}
}

// TestGuardSpecBindings_ScopeGated — only a network-base delete can orphan a
// binding. Deleting a zone/node override is free: the network floor (§7)
// guarantees the base still exists, so every binding still resolves through
// fall-through. The guard must therefore be a no-op for non-network scopes even
// when the spec is applied — otherwise it over-blocks override deletes that the
// internal scope-gated guards correctly allow.
func TestGuardSpecBindings_ScopeGated(t *testing.T) {
	net, _ := loadServiceFixture(t)
	if got := net.bindingsFor(SpecKindService, "rtd"); len(got) != 2 {
		t.Fatalf("precondition: rtd applied on 2 interfaces, got %v", got)
	}
	for _, scope := range []string{spec.ScopeZone, spec.ScopeNode} {
		if err := net.guardSpecBindings(scope, SpecKindService, "ServiceSpec", "rtd", false); err != nil {
			t.Errorf("scope %q: guard over-blocked an override delete of an applied spec: %v", scope, err)
		}
	}
	// Network scope still refuses — the guard is scope-gated, not disabled.
	if err := net.guardSpecBindings(spec.ScopeNetwork, SpecKindService, "ServiceSpec", "rtd", false); err == nil {
		t.Error("network scope: guard failed to refuse deleting an applied service")
	}
}

// TestBindingsFor_GeneralizesAcrossKinds — bindingsFor keys off DeriveSpecRef,
// so the same scan serves every binding op. An unbound spec name reports no
// bindings; the kind discriminates (a service name is not a filter binding).
func TestBindingsFor_GeneralizesAcrossKinds(t *testing.T) {
	net, _ := loadServiceFixture(t)

	if got := net.bindingsFor(SpecKindService, "no-such-service"); got != nil {
		t.Errorf("unbound service: bindings=%v, want nil", got)
	}
	// "rtd" is a service binding, not a filter binding — kind must discriminate.
	if got := net.bindingsFor(SpecKindFilter, "rtd"); got != nil {
		t.Errorf("rtd as filter: bindings=%v, want nil (wrong kind)", got)
	}
	// Canonicalization: the raw step casing ("rtd") and a typed query
	// ("RTD") resolve to the same bindings.
	if got := net.bindingsFor(SpecKindService, "RTD"); len(got) != 2 {
		t.Errorf("canonical query: bindings=%v, want 2", got)
	}
}

// TestInterfaceFromStepURL covers the interface extraction directly, including
// the no-interface fallback.
func TestInterfaceFromStepURL(t *testing.T) {
	cases := map[string]string{
		"/interfaces/Ethernet0/apply-service": "Ethernet0",
		"interfaces/Ethernet4/bind-qos":       "Ethernet4",
		"/setup-device":                       "",
		"/interfaces":                         "",
	}
	for url, want := range cases {
		if got := interfaceFromStepURL(url); got != want {
			t.Errorf("interfaceFromStepURL(%q) = %q, want %q", url, got, want)
		}
	}
}

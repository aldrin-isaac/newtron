package network

import (
	"errors"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// TestSpecDependencyChecks exercises the referential-integrity framework
// end-to-end through the network CRUD methods: the reverse guard (delete
// refused while referenced) and the forward guard (create rejected for a
// dangling reference). Pairs with the framework unit tests in pkg/.../spec.
func TestSpecDependencyChecks(t *testing.T) {
	n := loadTestNetwork(t)

	// --- Reverse guard, incl. the prefix-list↔service-routing path the old
	//     hand-coded scan missed (the concrete gap this work closes) ---
	if err := n.CreatePrefixList("", "", "BOGONS", []string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("CreatePrefixList: %v", err)
	}
	// A routed service that references the prefix list only via routing import.
	svc := &spec.ServiceSpec{ServiceType: "routed", Routing: &spec.RoutingSpec{ImportPrefixList: "BOGONS"}}
	if err := n.CreateService("", "", "EDGE", svc); err != nil {
		t.Fatalf("CreateService (valid prefix-list ref): %v", err)
	}

	err := n.DeletePrefixList("", "", "BOGONS")
	var conflict *util.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("DeletePrefixList while a service references it via routing: got %v, want *util.ConflictError (409)", err)
	}
	// Once the consumer is gone, the delete succeeds.
	if err := n.DeleteService("", "", "EDGE"); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	if err := n.DeletePrefixList("", "", "BOGONS"); err != nil {
		t.Errorf("DeletePrefixList after removing the consumer: %v", err)
	}

	// --- Forward guard: a spec that references something nonexistent is rejected ---
	// evpn-routed needs only an ipvpn (no macvpn), isolating the reference check
	// from the service-type shape constraints.
	err = n.CreateService("", "", "BAD", &spec.ServiceSpec{ServiceType: "evpn-routed", IPVPN: "GHOST"})
	var refErr *spec.ReferenceError
	if !errors.As(err, &refErr) {
		t.Fatalf("CreateService with a dangling ipvpn ref: got %v, want *spec.ReferenceError (400)", err)
	}

	// The same spec succeeds once the dependency exists.
	if err := n.CreateIPVPN("", "", "GHOST", &spec.IPVPNSpec{L3VNI: 1001}); err != nil {
		t.Fatalf("CreateIPVPN: %v", err)
	}
	if err := n.CreateService("", "", "BAD", &spec.ServiceSpec{ServiceType: "evpn-routed", IPVPN: "GHOST"}); err != nil {
		t.Errorf("CreateService after creating its ipvpn dependency: %v", err)
	}
}

package node

import (
	"context"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestApplyService_RecordsSourceFilterOnACLIntent pins the generation side of
// the spec-provenance feature: when a service applies an ingress/egress filter,
// the resulting (content-hash-named) create-acl intent records the SOURCE
// filter spec name in sonic.FieldFilter — so a client can map the hashed ACL
// back to its filter without reversing the hash. Pairs with the consumer-side
// DeriveSpecRef test in pkg/newtron.
func TestApplyService_RecordsSourceFilterOnACLIntent(t *testing.T) {
	n := newTestAbstract()
	sp := n.SpecProvider.(*testSpecProvider)

	sp.filterSpecs["mgmt-in"] = &spec.FilterSpec{
		Type: "ipv4",
		Rules: []*spec.FilterRule{
			{Sequence: 10, SrcIP: "10.0.0.0/8", Action: "permit"},
		},
	}
	sp.services["ACLSVC"] = &spec.ServiceSpec{
		ServiceType:   "routed",
		IngressFilter: "mgmt-in",
	}

	iface, err := n.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	if _, err := iface.ApplyService(context.Background(), "ACLSVC", ApplyServiceOpts{
		IPAddress: "10.1.0.1/31",
	}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}

	// Find the create-acl intent and assert it carries the source filter name.
	var found bool
	for resource, fields := range n.ConfigDB().NewtronIntent {
		if fields["operation"] != sonic.OpCreateACL {
			continue
		}
		found = true
		if got := fields[sonic.FieldFilter]; got != "mgmt-in" {
			t.Errorf("create-acl intent %q: %s = %q, want the source filter 'mgmt-in'",
				resource, sonic.FieldFilter, got)
		}
	}
	if !found {
		t.Fatal("no create-acl intent was written — the service's ingress filter did not generate an ACL")
	}
}

package spec

import (
	"sort"
	"testing"
)

// sampleSpecs builds an OverridableSpecs with a small reference web:
//
//	PrefixListSpec BOGONS  ← FilterSpec MGMT (rule), RoutePolicy RP1 (rule),
//	                          ServiceSpec TRANSIT (routing import_prefix_list)
//	FilterSpec MGMT        ← ServiceSpec TRANSIT (ingress_filter)
//	IPVPNSpec IRB          ← ServiceSpec TRANSIT (ipvpn)
func sampleSpecs() *OverridableSpecs {
	return &OverridableSpecs{
		PrefixLists: map[string][]string{"BOGONS": {"10.0.0.0/8"}},
		Filters: map[string]*FilterSpec{
			"MGMT": {Type: "ipv4", Rules: []*FilterRule{{Sequence: 10, SrcPrefixList: "BOGONS", Action: "permit"}}},
		},
		RoutePolicies: map[string]*RoutePolicy{
			"RP1": {Rules: []*RoutePolicyRule{{Sequence: 10, Action: "permit", PrefixList: "BOGONS"}}},
		},
		IPVPNs: map[string]*IPVPNSpec{"IRB": {L3VNI: 1001}},
		Services: map[string]*ServiceSpec{
			"TRANSIT": {
				ServiceType:   "evpn-irb",
				IPVPN:         "IRB",
				IngressFilter: "MGMT",
				Routing:       &RoutingSpec{ImportPrefixList: "BOGONS"},
			},
		},
	}
}

func TestCollectRefs_NestedAndCanonical(t *testing.T) {
	svc := &ServiceSpec{
		IPVPN:         "irb",                                     // authored lowercase
		IngressFilter: "mgmt",                                    // authored lowercase
		Routing:       &RoutingSpec{ImportPrefixList: "bo-gons"}, // hyphen + lowercase
	}
	got := map[string]string{} // Kind → Name
	for _, r := range CollectRefs(svc) {
		got[r.Kind] = r.Name
	}
	want := map[string]string{
		"IPVPNSpec":      "IRB",     // canonicalized
		"FilterSpec":     "MGMT",    // canonicalized
		"PrefixListSpec": "BO_GONS", // hyphen→underscore + upper (reached via nested Routing)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("CollectRefs: %s = %q, want %q (all refs: %v)", k, got[k], v, got)
		}
	}
}

func TestFindConsumers(t *testing.T) {
	o := sampleSpecs()

	// BOGONS is referenced by the filter, the route policy, AND the service.
	got := consumerKinds(o.FindConsumers("PrefixListSpec", "BOGONS"))
	want := []string{"FilterSpec/MGMT", "RoutePolicy/RP1", "ServiceSpec/TRANSIT"}
	if !equalSorted(got, want) {
		t.Errorf("consumers of PrefixListSpec/BOGONS = %v, want %v", got, want)
	}

	// IRB is referenced only by the service.
	if got := consumerKinds(o.FindConsumers("IPVPNSpec", "IRB")); !equalSorted(got, []string{"ServiceSpec/TRANSIT"}) {
		t.Errorf("consumers of IPVPNSpec/IRB = %v, want [ServiceSpec/TRANSIT]", got)
	}

	// A spec nobody references is freely deletable.
	if got := o.FindConsumers("ServiceSpec", "TRANSIT"); len(got) != 0 {
		t.Errorf("consumers of ServiceSpec/TRANSIT = %v, want none", got)
	}
}

func TestMissingRefs(t *testing.T) {
	o := sampleSpecs()

	// A service whose refs all exist → no missing.
	ok := &ServiceSpec{IPVPN: "IRB", IngressFilter: "MGMT", Routing: &RoutingSpec{ImportPrefixList: "BOGONS"}}
	if m := o.MissingRefs(ok); len(m) != 0 {
		t.Errorf("MissingRefs(valid) = %v, want none", m)
	}

	// A service referencing a non-existent IP-VPN and prefix list (authored
	// lowercase) → both flagged, canonicalized.
	bad := &ServiceSpec{IPVPN: "ghost", Routing: &RoutingSpec{ImportPrefixList: "nope"}}
	missing := map[string]bool{}
	for _, r := range o.MissingRefs(bad) {
		missing[r.Kind+"/"+r.Name] = true
	}
	if !missing["IPVPNSpec/GHOST"] || !missing["PrefixListSpec/NOPE"] {
		t.Errorf("MissingRefs(bad) = %v, want IPVPNSpec/GHOST + PrefixListSpec/NOPE", missing)
	}
	// The existing ingress filter must NOT be flagged.
	if missing["FilterSpec/MGMT"] {
		t.Error("MissingRefs flagged an existing filter")
	}
}

func consumerKinds(cs []Consumer) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Kind + "/" + c.Name
	}
	return out
}

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

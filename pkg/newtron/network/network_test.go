package network

import (
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// ============================================================================
// Network ListServices/ListFilters Tests (minimal)
// ============================================================================

func TestNetwork_ListServicesEmpty(t *testing.T) {
	// Test with minimal network (no specs loaded)
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: make(map[string]*spec.ServiceSpec),
		},
	}
	services := n.ListServices()
	if len(services) != 0 {
		t.Errorf("ListServices() = %v, want empty", services)
	}
}

func TestNetwork_ListFiltersEmpty(t *testing.T) {
	// Test with minimal network (no specs loaded)
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Filters: make(map[string]*spec.FilterSpec),
		},
	}
	filters := n.ListFilters()
	if len(filters) != 0 {
		t.Errorf("ListFilters() = %v, want empty", filters)
	}
}

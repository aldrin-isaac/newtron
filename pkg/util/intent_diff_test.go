package util

import (
	"reflect"
	"testing"
)

func TestDiffIntentRecords(t *testing.T) {
	base := IntentRecords{
		"device":                    {"operation": "setup-device", "state": "actuated"},
		"vlan|100":                  {"operation": "create-vlan", "vlan_id": "100"},
		"interface|Vlan100|service": {"operation": "apply-service", "vrf_name": "Vrf_A"},
	}

	t.Run("identical → empty", func(t *testing.T) {
		// A deep copy so we compare by value, not identity.
		cur := IntentRecords{
			"device":                    {"operation": "setup-device", "state": "actuated"},
			"vlan|100":                  {"operation": "create-vlan", "vlan_id": "100"},
			"interface|Vlan100|service": {"operation": "apply-service", "vrf_name": "Vrf_A"},
		}
		d := DiffIntentRecords(base, cur)
		if !d.Empty() {
			t.Fatalf("expected empty diff, got %+v", d)
		}
	})

	t.Run("residual record → Added", func(t *testing.T) {
		cur := IntentRecords{
			"device":                    {"operation": "setup-device", "state": "actuated"},
			"vlan|100":                  {"operation": "create-vlan", "vlan_id": "100"},
			"interface|Vlan100|service": {"operation": "apply-service", "vrf_name": "Vrf_A"},
			"ipvpn|IPVPN":               {"operation": "bind-ipvpn", "vrf_name": "Vrf_A"}, // leaked
		}
		d := DiffIntentRecords(base, cur)
		if !reflect.DeepEqual(d.Added, []string{"ipvpn|IPVPN"}) {
			t.Fatalf("expected Added=[ipvpn|IPVPN], got %+v", d)
		}
		if len(d.Removed) != 0 || len(d.Changed) != 0 {
			t.Fatalf("only Added expected, got %+v", d)
		}
	})

	t.Run("missing record → Removed", func(t *testing.T) {
		cur := IntentRecords{
			"device":   {"operation": "setup-device", "state": "actuated"},
			"vlan|100": {"operation": "create-vlan", "vlan_id": "100"},
		}
		d := DiffIntentRecords(base, cur)
		if !reflect.DeepEqual(d.Removed, []string{"interface|Vlan100|service"}) {
			t.Fatalf("expected Removed=[interface|Vlan100|service], got %+v", d)
		}
	})

	t.Run("changed fields → Changed", func(t *testing.T) {
		cur := IntentRecords{
			"device":                    {"operation": "setup-device", "state": "actuated"},
			"vlan|100":                  {"operation": "create-vlan", "vlan_id": "100"},
			"interface|Vlan100|service": {"operation": "apply-service", "vrf_name": "Vrf_B"}, // moved
		}
		d := DiffIntentRecords(base, cur)
		if !reflect.DeepEqual(d.Changed, []string{"interface|Vlan100|service"}) {
			t.Fatalf("expected Changed=[interface|Vlan100|service], got %+v", d)
		}
	})

	t.Run("Summary names the residual", func(t *testing.T) {
		cur := IntentRecords{"device": base["device"], "vlan|999": {"operation": "create-vlan"}}
		d := DiffIntentRecords(base, cur)
		s := d.Summary("baseline")
		if s == "" || d.Empty() {
			t.Fatalf("expected a non-empty divergence summary, got %q (%+v)", s, d)
		}
	})
}

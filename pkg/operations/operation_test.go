package operations

import (
	"strings"
	"testing"
	"time"
)

func TestNewChangeSet(t *testing.T) {
	cs := NewChangeSet("leaf1-ny", "service.apply")

	if cs.Device != "leaf1-ny" {
		t.Errorf("Device = %q, want %q", cs.Device, "leaf1-ny")
	}
	if cs.Operation != "service.apply" {
		t.Errorf("Operation = %q, want %q", cs.Operation, "service.apply")
	}
	if len(cs.Changes) != 0 {
		t.Errorf("Changes count = %d, want %d", len(cs.Changes), 0)
	}
	if cs.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
	if !cs.DryRun {
		t.Error("DryRun should be true by default")
	}
}

func TestChangeSet_AddChange(t *testing.T) {
	cs := NewChangeSet("test", "test")

	cs.AddChange("PORT", "Ethernet0", ChangeAdd, nil, map[string]string{"mtu": "9100"})

	if len(cs.Changes) != 1 {
		t.Fatalf("Changes count = %d, want %d", len(cs.Changes), 1)
	}

	c := cs.Changes[0]
	if c.Table != "PORT" {
		t.Errorf("Table = %q, want %q", c.Table, "PORT")
	}
	if c.Key != "Ethernet0" {
		t.Errorf("Key = %q, want %q", c.Key, "Ethernet0")
	}
	if c.Operation != ChangeAdd {
		t.Errorf("Operation = %q, want %q", c.Operation, ChangeAdd)
	}
	if c.OldValue != nil {
		t.Errorf("OldValue should be nil")
	}
	if c.NewValue == nil {
		t.Error("NewValue should not be nil")
	}
}

func TestChangeSet_IsEmpty(t *testing.T) {
	cs := NewChangeSet("test", "test")

	if !cs.IsEmpty() {
		t.Error("New ChangeSet should be empty")
	}

	cs.AddChange("PORT", "Ethernet0", ChangeAdd, nil, nil)

	if cs.IsEmpty() {
		t.Error("ChangeSet with changes should not be empty")
	}
}

func TestChangeSet_String(t *testing.T) {
	t.Run("empty changeset", func(t *testing.T) {
		cs := NewChangeSet("test", "test")
		str := cs.String()
		if str != "No changes" {
			t.Errorf("String() = %q, want %q", str, "No changes")
		}
	})

	t.Run("with changes", func(t *testing.T) {
		cs := NewChangeSet("test", "test")
		cs.AddChange("PORT", "Ethernet0", ChangeAdd, nil, nil)
		cs.AddChange("VLAN", "Vlan100", ChangeModify, nil, nil)
		cs.AddChange("VRF", "Vrf_CUST", ChangeDelete, nil, nil)

		str := cs.String()
		if !strings.Contains(str, "[ADD]") {
			t.Error("String should contain [ADD]")
		}
		if !strings.Contains(str, "[MOD]") {
			t.Error("String should contain [MOD]")
		}
		if !strings.Contains(str, "[DEL]") {
			t.Error("String should contain [DEL]")
		}
		if !strings.Contains(str, "PORT|Ethernet0") {
			t.Error("String should contain PORT|Ethernet0")
		}
	})
}

func TestChangeConstants(t *testing.T) {
	if ChangeAdd != "add" {
		t.Errorf("ChangeAdd = %q, want %q", ChangeAdd, "add")
	}
	if ChangeModify != "modify" {
		t.Errorf("ChangeModify = %q, want %q", ChangeModify, "modify")
	}
	if ChangeDelete != "delete" {
		t.Errorf("ChangeDelete = %q, want %q", ChangeDelete, "delete")
	}
}

func TestBaseOperation_Validation(t *testing.T) {
	t.Run("not validated", func(t *testing.T) {
		b := &BaseOperation{}
		if b.IsValidated() {
			t.Error("Should not be validated initially")
		}
		if err := b.RequireValidated(); err == nil {
			t.Error("RequireValidated should return error")
		}
	})

	t.Run("validated", func(t *testing.T) {
		b := &BaseOperation{}
		b.MarkValidated()

		if !b.IsValidated() {
			t.Error("Should be validated after MarkValidated")
		}
		if err := b.RequireValidated(); err != nil {
			t.Errorf("RequireValidated should not return error: %v", err)
		}
	})
}

func TestSplitPorts(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"Ethernet0", []string{"Ethernet0"}},
		{"Ethernet0,Ethernet4", []string{"Ethernet0", "Ethernet4"}},
		{"Ethernet0, Ethernet4", []string{"Ethernet0", "Ethernet4"}},
		{"Ethernet0,  Ethernet4,Ethernet8", []string{"Ethernet0", "Ethernet4", "Ethernet8"}},
		{" , ", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitPorts(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("splitPorts(%q) = %v (len %d), want %v (len %d)",
					tt.input, got, len(got), tt.expected, len(tt.expected))
				return
			}
			for i, v := range got {
				if v != tt.expected[i] {
					t.Errorf("splitPorts(%q)[%d] = %q, want %q", tt.input, i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestChangeSet_MultipleOperations(t *testing.T) {
	cs := NewChangeSet("leaf1-ny", "service.apply")

	// Add typical service apply changes
	cs.AddChange("VRF", "customer-l3-Ethernet0", ChangeAdd, nil, map[string]string{
		"vni": "10001",
	})
	cs.AddChange("INTERFACE", "Ethernet0", ChangeModify, nil, map[string]string{
		"vrf_name": "customer-l3-Ethernet0",
	})
	cs.AddChange("INTERFACE", "Ethernet0|10.1.1.1/30", ChangeAdd, nil, nil)
	cs.AddChange("ACL_TABLE", "customer-l3-Ethernet0-in", ChangeAdd, nil, map[string]string{
		"type":  "L3",
		"stage": "ingress",
		"ports": "Ethernet0",
	})
	cs.AddChange("ACL_RULE", "customer-l3-Ethernet0-in|RULE_100", ChangeAdd, nil, map[string]string{
		"packet_action": "FORWARD",
	})

	if len(cs.Changes) != 5 {
		t.Errorf("Changes count = %d, want %d", len(cs.Changes), 5)
	}

	// Verify ordering is preserved
	if cs.Changes[0].Table != "VRF" {
		t.Error("First change should be VRF")
	}
	if cs.Changes[4].Table != "ACL_RULE" {
		t.Error("Last change should be ACL_RULE")
	}
}

func TestChangeSet_Timestamp(t *testing.T) {
	before := time.Now()
	cs := NewChangeSet("test", "test")
	after := time.Now()

	if cs.Timestamp.Before(before) || cs.Timestamp.After(after) {
		t.Errorf("Timestamp %v should be between %v and %v", cs.Timestamp, before, after)
	}
}

func TestChange_Structure(t *testing.T) {
	// Test that Change struct properly holds all field types
	c := Change{
		Table:     "PORT",
		Key:       "Ethernet0",
		Operation: ChangeModify,
		OldValue:  map[string]string{"mtu": "1500"},
		NewValue:  map[string]string{"mtu": "9100"},
	}

	if c.Table != "PORT" {
		t.Errorf("Table = %q", c.Table)
	}
	if c.Key != "Ethernet0" {
		t.Errorf("Key = %q", c.Key)
	}
	if c.Operation != ChangeModify {
		t.Errorf("Operation = %q", c.Operation)
	}

	oldVal, ok := c.OldValue.(map[string]string)
	if !ok || oldVal["mtu"] != "1500" {
		t.Errorf("OldValue = %v", c.OldValue)
	}

	newVal, ok := c.NewValue.(map[string]string)
	if !ok || newVal["mtu"] != "9100" {
		t.Errorf("NewValue = %v", c.NewValue)
	}
}

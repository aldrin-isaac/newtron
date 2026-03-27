package sonic

import (
	"strings"
	"testing"
)

// ============================================================================
// FieldConstraint.Check tests
// ============================================================================

func TestCheck_FieldInt(t *testing.T) {
	fc := FieldConstraint{Type: FieldInt, Range: intRange(1, 4094)}

	tests := []struct {
		value string
		ok    bool
	}{
		{"100", true},
		{"1", true},
		{"4094", true},
		{"0", false},
		{"4095", false},
		{"-1", false},
		{"abc", false},
		{"", false},
	}

	for _, tt := range tests {
		err := fc.Check(tt.value)
		if tt.ok && err != nil {
			t.Errorf("Check(%q) = %v, want nil", tt.value, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("Check(%q) = nil, want error", tt.value)
		}
	}
}

func TestCheck_FieldInt_NoRange(t *testing.T) {
	fc := FieldConstraint{Type: FieldInt}
	if err := fc.Check("42"); err != nil {
		t.Errorf("Check(42) with no range = %v, want nil", err)
	}
	if err := fc.Check("abc"); err == nil {
		t.Error("Check(abc) with no range = nil, want error")
	}
}

func TestCheck_FieldEnum(t *testing.T) {
	fc := FieldConstraint{Type: FieldEnum, Enum: []string{"up", "down"}}

	if err := fc.Check("up"); err != nil {
		t.Errorf("Check(up) = %v, want nil", err)
	}
	if err := fc.Check("down"); err != nil {
		t.Errorf("Check(down) = %v, want nil", err)
	}
	if err := fc.Check("sideways"); err == nil {
		t.Error("Check(sideways) = nil, want error")
	}
}

func TestCheck_FieldBool(t *testing.T) {
	fc := FieldConstraint{Type: FieldBool}

	if err := fc.Check("true"); err != nil {
		t.Errorf("Check(true) = %v", err)
	}
	if err := fc.Check("false"); err != nil {
		t.Errorf("Check(false) = %v", err)
	}
	if err := fc.Check("yes"); err == nil {
		t.Error("Check(yes) = nil, want error")
	}
}

func TestCheck_FieldIP(t *testing.T) {
	fc := FieldConstraint{Type: FieldIP}

	if err := fc.Check("10.0.0.1"); err != nil {
		t.Errorf("Check(10.0.0.1) = %v", err)
	}
	if err := fc.Check("not-an-ip"); err == nil {
		t.Error("Check(not-an-ip) = nil, want error")
	}
	if err := fc.Check(""); err == nil {
		t.Error("Check('') = nil, want error")
	}
}

func TestCheck_FieldCIDR(t *testing.T) {
	fc := FieldConstraint{Type: FieldCIDR}

	if err := fc.Check("10.0.0.0/24"); err != nil {
		t.Errorf("Check(10.0.0.0/24) = %v", err)
	}
	if err := fc.Check("10.0.0.1/32"); err != nil {
		t.Errorf("Check(10.0.0.1/32) = %v", err)
	}
	if err := fc.Check("10.0.0.1"); err == nil {
		t.Error("Check(10.0.0.1) without mask = nil, want error")
	}
	if err := fc.Check("garbage"); err == nil {
		t.Error("Check(garbage) = nil, want error")
	}
}

func TestCheck_FieldMAC(t *testing.T) {
	fc := FieldConstraint{Type: FieldMAC}

	if err := fc.Check("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Errorf("Check(valid MAC) = %v", err)
	}
	if err := fc.Check("AA:BB:CC:DD:EE:FF"); err != nil {
		t.Errorf("Check(upper MAC) = %v", err)
	}
	if err := fc.Check("not-a-mac"); err == nil {
		t.Error("Check(not-a-mac) = nil, want error")
	}
}

func TestCheck_FieldString(t *testing.T) {
	fc := FieldConstraint{Type: FieldString}
	if err := fc.Check("anything goes"); err != nil {
		t.Errorf("Check(string) = %v", err)
	}
}

func TestCheck_FieldString_WithPattern(t *testing.T) {
	fc := FieldConstraint{Type: FieldString, Pattern: `^Vlan\d+$`}

	if err := fc.Check("Vlan100"); err != nil {
		t.Errorf("Check(Vlan100) = %v", err)
	}
	if err := fc.Check("Ethernet0"); err == nil {
		t.Error("Check(Ethernet0) against Vlan pattern = nil, want error")
	}
}

// ============================================================================
// Table schema validation tests
// ============================================================================

func TestValidateEntry_VLAN_ValidKey(t *testing.T) {
	err := Schema["VLAN"].ValidateEntry("VLAN", "Vlan100", map[string]string{
		"vlanid": "100",
	})
	if err != nil {
		t.Errorf("valid VLAN entry: %v", err)
	}
}

func TestValidateEntry_VLAN_InvalidID(t *testing.T) {
	err := Schema["VLAN"].ValidateEntry("VLAN", "Vlan100", map[string]string{
		"vlanid": "99999",
	})
	if err == nil {
		t.Error("VLAN with vlanid=99999 should fail")
	}
}

func TestValidateEntry_VLAN_InvalidKey(t *testing.T) {
	err := Schema["VLAN"].ValidateEntry("VLAN", "Vlan0", map[string]string{
		"vlanid": "0",
	})
	if err == nil {
		t.Error("Vlan0 should fail key pattern")
	}
}

func TestValidateEntry_VLAN_Vlan1_Invalid(t *testing.T) {
	err := Schema["VLAN"].ValidateEntry("VLAN", "Vlan1", map[string]string{
		"vlanid": "1",
	})
	if err == nil {
		t.Error("Vlan1 should fail (reserved)")
	}
}

func TestValidateEntry_VLAN_Vlan4094_Valid(t *testing.T) {
	err := Schema["VLAN"].ValidateEntry("VLAN", "Vlan4094", map[string]string{
		"vlanid": "4094",
	})
	if err != nil {
		t.Errorf("Vlan4094 should be valid: %v", err)
	}
}

func TestValidateEntry_VLAN_UnknownField(t *testing.T) {
	err := Schema["VLAN"].ValidateEntry("VLAN", "Vlan100", map[string]string{
		"vlanid":  "100",
		"bogus":   "field",
	})
	if err == nil {
		t.Error("unknown field should fail")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error should mention unknown field: %v", err)
	}
}

func TestValidateEntry_BGP_NEIGHBOR_Valid(t *testing.T) {
	err := Schema["BGP_NEIGHBOR"].ValidateEntry("BGP_NEIGHBOR", "default|10.1.1.2", map[string]string{
		"asn":          "65001",
		"admin_status": "up",
		"local_addr":   "10.0.0.1",
	})
	if err != nil {
		t.Errorf("valid BGP_NEIGHBOR: %v", err)
	}
}

func TestValidateEntry_BGP_NEIGHBOR_InvalidASN(t *testing.T) {
	err := Schema["BGP_NEIGHBOR"].ValidateEntry("BGP_NEIGHBOR", "default|10.1.1.2", map[string]string{
		"asn": "0",
	})
	if err == nil {
		t.Error("ASN=0 should fail")
	}
}

func TestValidateEntry_BGP_NEIGHBOR_InvalidIP(t *testing.T) {
	err := Schema["BGP_NEIGHBOR"].ValidateEntry("BGP_NEIGHBOR", "default|10.1.1.2", map[string]string{
		"local_addr": "not-an-ip",
	})
	if err == nil {
		t.Error("invalid local_addr should fail")
	}
}

func TestValidateEntry_ACL_RULE_Valid(t *testing.T) {
	err := Schema["ACL_RULE"].ValidateEntry("ACL_RULE", "myacl|RULE_10", map[string]string{
		"PRIORITY":      "9990",
		"PACKET_ACTION": "FORWARD",
		"SRC_IP":        "10.0.0.0/8",
	})
	if err != nil {
		t.Errorf("valid ACL_RULE: %v", err)
	}
}

func TestValidateEntry_ACL_RULE_InvalidPriority(t *testing.T) {
	err := Schema["ACL_RULE"].ValidateEntry("ACL_RULE", "myacl|RULE_10", map[string]string{
		"PRIORITY":      "99999",
		"PACKET_ACTION": "FORWARD",
	})
	if err == nil {
		t.Error("priority 99999 should fail")
	}
}

func TestValidateEntry_INTERFACE_EmptyFields(t *testing.T) {
	// Key-only entry: INTERFACE|Ethernet0 with no fields (enable IP routing)
	err := Schema["INTERFACE"].ValidateEntry("INTERFACE", "Ethernet0", map[string]string{})
	if err != nil {
		t.Errorf("empty-field INTERFACE entry should be valid: %v", err)
	}
}

func TestValidateEntry_INTERFACE_IPSubEntry(t *testing.T) {
	err := Schema["INTERFACE"].ValidateEntry("INTERFACE", "Ethernet0|10.1.1.1/30", map[string]string{})
	if err != nil {
		t.Errorf("INTERFACE IP sub-entry should be valid: %v", err)
	}
}

func TestValidateEntry_SAG_GLOBAL(t *testing.T) {
	err := Schema["SAG_GLOBAL"].ValidateEntry("SAG_GLOBAL", "IPv4", map[string]string{
		"gwmac": "00:11:22:33:44:55",
	})
	if err != nil {
		t.Errorf("valid SAG_GLOBAL: %v", err)
	}
}

// ============================================================================
// ValidateChanges tests
// ============================================================================

func TestValidateChanges_ValidBatch(t *testing.T) {
	changes := []ConfigChange{
		{Table: "VLAN", Key: "Vlan100", Type: ChangeTypeAdd, Fields: map[string]string{"vlanid": "100"}},
		{Table: "VLAN_MEMBER", Key: "Vlan100|Ethernet0", Type: ChangeTypeAdd, Fields: map[string]string{"tagging_mode": "untagged"}},
		{Table: "INTERFACE", Key: "Ethernet0", Type: ChangeTypeAdd, Fields: map[string]string{}},
	}
	if err := ValidateChanges(changes); err != nil {
		t.Errorf("valid batch: %v", err)
	}
}

func TestValidateChanges_UnknownTable(t *testing.T) {
	changes := []ConfigChange{
		{Table: "MADE_UP_TABLE", Key: "foo", Type: ChangeTypeAdd, Fields: map[string]string{"x": "y"}},
	}
	err := ValidateChanges(changes)
	if err == nil {
		t.Error("unknown table should fail")
	}
	if !strings.Contains(err.Error(), "unknown table") {
		t.Errorf("error should mention unknown table: %v", err)
	}
}

func TestValidateChanges_DeleteSkipsFieldValidation(t *testing.T) {
	changes := []ConfigChange{
		{Table: "VLAN", Key: "Vlan100", Type: ChangeTypeDelete},
	}
	if err := ValidateChanges(changes); err != nil {
		t.Errorf("delete should skip field validation: %v", err)
	}
}

func TestValidateChanges_DeleteValidatesKey(t *testing.T) {
	changes := []ConfigChange{
		{Table: "VLAN", Key: "InvalidKey", Type: ChangeTypeDelete},
	}
	err := ValidateChanges(changes)
	if err == nil {
		t.Error("delete with invalid key should fail")
	}
}

func TestValidateChanges_MultipleErrors(t *testing.T) {
	changes := []ConfigChange{
		{Table: "VLAN", Key: "Vlan100", Type: ChangeTypeAdd, Fields: map[string]string{
			"vlanid": "99999",
			"bogus":  "field",
		}},
		{Table: "BGP_NEIGHBOR", Key: "default|10.1.1.2", Type: ChangeTypeAdd, Fields: map[string]string{
			"asn": "-1",
		}},
	}
	err := ValidateChanges(changes)
	if err == nil {
		t.Fatal("should have multiple errors")
	}
	// Should contain errors for vlanid range, unknown field, and invalid ASN
	errStr := err.Error()
	if !strings.Contains(errStr, "vlanid") {
		t.Errorf("should mention vlanid: %v", err)
	}
	if !strings.Contains(errStr, "bogus") {
		t.Errorf("should mention bogus field: %v", err)
	}
}

func TestValidateChanges_DeleteUnknownTablePasses(t *testing.T) {
	// Deletes of unknown tables pass — we don't know their key format
	changes := []ConfigChange{
		{Table: "UNKNOWN", Key: "anything", Type: ChangeTypeDelete},
	}
	if err := ValidateChanges(changes); err != nil {
		t.Errorf("delete of unknown table should pass: %v", err)
	}
}

func TestValidateChanges_AllowExtraFields(t *testing.T) {
	// NEWTRON_INTENT has AllowExtra: true — unknown fields should pass in ValidateChanges too
	changes := []ConfigChange{
		{Table: "NEWTRON_INTENT", Key: "Ethernet0", Type: ChangeTypeAdd, Fields: map[string]string{
			"state":        "actuated",
			"operation":    "apply-service",
			"service_name": "TRANSIT",
			"service_type": "routed",
			"ip_address":   "10.0.1.1/31",
			"bgp_neighbor": "10.0.1.0",
			"bgp_peer_as":  "65001",
			"peer_group":   "TRANSIT",
		}},
	}
	if err := ValidateChanges(changes); err != nil {
		t.Errorf("AllowExtra fields in ValidateChanges should pass: %v", err)
	}
}

// ============================================================================
// Schema coverage tests
// ============================================================================

// ============================================================================
// NEWTRON_INTENT table tests
// ============================================================================

func TestValidateEntry_NEWTRON_INTENT_Valid(t *testing.T) {
	// Service intent on interface — identity fields + operation-specific params
	err := Schema["NEWTRON_INTENT"].ValidateEntry("NEWTRON_INTENT", "Ethernet0", map[string]string{
		"state":        "actuated",
		"operation":    "apply-service",
		"service_name": "TRANSIT",
		"service_type": "routed",
		"vrf_name":     "CUSTOMER",
		"ip_address":   "10.0.1.1/31",
	})
	if err != nil {
		t.Errorf("valid service intent: %v", err)
	}
}

func TestValidateEntry_NEWTRON_INTENT_ExtraParamsAllowed(t *testing.T) {
	// Node-level intent with operation-specific params not in schema Fields map
	err := Schema["NEWTRON_INTENT"].ValidateEntry("NEWTRON_INTENT", "leaf1", map[string]string{
		"state":     "actuated",
		"operation": "setup-device",
		"hostname":  "leaf1",
		"bgp_asn":   "65001",
		"loopback":  "10.0.0.1/32",
	})
	if err != nil {
		t.Errorf("extra params should be allowed: %v", err)
	}
}

func TestValidateEntry_NEWTRON_INTENT_InvalidOperation(t *testing.T) {
	err := Schema["NEWTRON_INTENT"].ValidateEntry("NEWTRON_INTENT", "leaf1", map[string]string{
		"state":     "actuated",
		"operation": "unknown-op",
	})
	if err == nil {
		t.Error("unknown operation should fail")
	}
}

func TestValidateEntry_NEWTRON_INTENT_InvalidState(t *testing.T) {
	err := Schema["NEWTRON_INTENT"].ValidateEntry("NEWTRON_INTENT", "Ethernet0", map[string]string{
		"state":     "pending",
		"operation": "apply-service",
	})
	if err == nil {
		t.Error("invalid state should fail")
	}
}

// ============================================================================
// NEWTRON_HISTORY table tests
// ============================================================================

func TestValidateEntry_NEWTRON_HISTORY_Valid(t *testing.T) {
	err := Schema["NEWTRON_HISTORY"].ValidateEntry("NEWTRON_HISTORY", "leaf1|42", map[string]string{
		"holder":     "admin@mgmt",
		"timestamp":  "2026-03-15T10:30:00Z",
		"operations": `[{"name":"device.create-vlan"}]`,
	})
	if err != nil {
		t.Errorf("valid NEWTRON_HISTORY: %v", err)
	}
}

func TestValidateEntry_NEWTRON_HISTORY_InvalidKey(t *testing.T) {
	err := Schema["NEWTRON_HISTORY"].ValidateEntry("NEWTRON_HISTORY", "leaf1", map[string]string{
		"holder":     "admin@mgmt",
		"timestamp":  "2026-03-15T10:30:00Z",
		"operations": `[]`,
	})
	if err == nil {
		t.Error("key without sequence should fail")
	}
}

func TestValidateEntry_NEWTRON_HISTORY_UnknownField(t *testing.T) {
	err := Schema["NEWTRON_HISTORY"].ValidateEntry("NEWTRON_HISTORY", "leaf1|1", map[string]string{
		"holder":     "admin@mgmt",
		"timestamp":  "2026-03-15T10:30:00Z",
		"operations": `[]`,
		"bogus":      "field",
	})
	if err == nil {
		t.Error("unknown field should fail")
	}
}

// NEWTRON_SETTINGS table tests
// ============================================================================

func TestValidateEntry_NEWTRON_SETTINGS_Valid(t *testing.T) {
	err := Schema["NEWTRON_SETTINGS"].ValidateEntry("NEWTRON_SETTINGS", "global", map[string]string{
		"max_history": "20",
	})
	if err != nil {
		t.Errorf("valid entry should pass: %v", err)
	}
}

func TestValidateEntry_NEWTRON_SETTINGS_InvalidKey(t *testing.T) {
	err := Schema["NEWTRON_SETTINGS"].ValidateEntry("NEWTRON_SETTINGS", "local", map[string]string{
		"max_history": "10",
	})
	if err == nil {
		t.Error("non-global key should fail")
	}
}

func TestValidateEntry_NEWTRON_SETTINGS_OutOfRange(t *testing.T) {
	err := Schema["NEWTRON_SETTINGS"].ValidateEntry("NEWTRON_SETTINGS", "global", map[string]string{
		"max_history": "999",
	})
	if err == nil {
		t.Error("max_history > 100 should fail")
	}
}

func TestSchema_AllTablesHaveFields(t *testing.T) {
	// Tables with empty Fields maps are valid (key-only entries like ROUTE_REDISTRIBUTE)
	for table, schema := range Schema {
		if schema.Fields == nil {
			t.Errorf("table %s has nil Fields map (should be empty map)", table)
		}
	}
}

func TestSchema_KnownTables(t *testing.T) {
	tables := KnownTables()
	if len(tables) < 30 {
		t.Errorf("expected at least 30 tables, got %d", len(tables))
	}

	// Verify critical tables are present
	required := []string{
		"VLAN", "VLAN_MEMBER", "VLAN_INTERFACE", "VRF", "INTERFACE",
		"BGP_GLOBALS", "BGP_NEIGHBOR", "BGP_NEIGHBOR_AF",
		"VXLAN_TUNNEL", "VXLAN_EVPN_NVO", "VXLAN_TUNNEL_MAP",
		"ACL_TABLE", "ACL_RULE",
		"NEWTRON_INTENT", "ROUTE_MAP", "PREFIX_SET",
	}
	for _, r := range required {
		if !IsKnownTable(r) {
			t.Errorf("required table %s missing from schema", r)
		}
	}
}

func TestSchema_VLANKeyPattern_BoundaryValues(t *testing.T) {
	schema := Schema["VLAN"]
	tests := []struct {
		key string
		ok  bool
	}{
		{"Vlan2", true},
		{"Vlan100", true},
		{"Vlan999", true},
		{"Vlan4094", true},
		{"Vlan1", false},    // reserved
		{"Vlan0", false},
		{"Vlan4095", false},
		{"Vlan5000", false},
		{"NotAVlan", false},
	}

	for _, tt := range tests {
		err := schema.ValidateEntry("VLAN", tt.key, map[string]string{"vlanid": "100"})
		hasKeyErr := err != nil && strings.Contains(err.Error(), "invalid key format")
		if tt.ok && hasKeyErr {
			t.Errorf("key %s should be valid, got key error: %v", tt.key, err)
		}
		if !tt.ok && !hasKeyErr {
			t.Errorf("key %s should fail key validation, got: %v", tt.key, err)
		}
	}
}

func TestSchema_BGP_NEIGHBOR_AF_KeyPattern(t *testing.T) {
	schema := Schema["BGP_NEIGHBOR_AF"]
	tests := []struct {
		key string
		ok  bool
	}{
		{"default|10.1.1.2|ipv4_unicast", true},
		{"customer|10.1.1.2|l2vpn_evpn", true},
		{"default|10.1.1.2|invalid_af", false},
	}

	for _, tt := range tests {
		err := schema.ValidateEntry("BGP_NEIGHBOR_AF", tt.key, map[string]string{})
		hasKeyErr := err != nil && strings.Contains(err.Error(), "invalid key format")
		if tt.ok && hasKeyErr {
			t.Errorf("key %s should be valid: %v", tt.key, err)
		}
		if !tt.ok && !hasKeyErr {
			t.Errorf("key %s should fail key validation", tt.key)
		}
	}
}

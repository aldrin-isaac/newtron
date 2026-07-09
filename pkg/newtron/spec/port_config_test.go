package spec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestPortConfig_RoundTrip pins that a topology device's port config loads into
// the typed PortConfig (numeric mtu, enum admin_status) and renders back to the
// CONFIG_DB PORT-table string fields SONiC stores — the normalize-at-the-
// boundary contract.
func TestPortConfig_RoundTrip(t *testing.T) {
	// Load: the on-disk topology.json shape — mtu is a JSON number.
	in := []byte(`{"ports":{"Ethernet0":{"admin_status":"up","mtu":9100},"Ethernet4":{"admin_status":"down","speed":"100G"}}}`)
	var dev TopologyNode
	if err := json.Unmarshal(in, &dev); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	e0 := dev.Ports["Ethernet0"]
	if e0 == nil || e0.AdminStatus != "up" || e0.MTU != 9100 {
		t.Fatalf("Ethernet0: got %+v, want admin_status=up mtu=9100", e0)
	}

	// Fields(): typed → CONFIG_DB string hash (mtu 9100 → "9100"), unset omitted.
	if got, want := e0.Fields(), (map[string]string{"admin_status": "up", "mtu": "9100"}); !reflect.DeepEqual(got, want) {
		t.Errorf("Ethernet0.Fields() = %v, want %v", got, want)
	}
	// speed renders in SONiC's Mbps form — the authored "100G" verbatim was
	// the RCA-050 defect this test used to pin as correct.
	if got, want := dev.Ports["Ethernet4"].Fields(), (map[string]string{"admin_status": "down", "speed": "100000"}); !reflect.DeepEqual(got, want) {
		t.Errorf("Ethernet4.Fields() = %v, want %v", got, want)
	}

	// Empty config and nil receiver both yield an empty (non-nil) field map.
	if f := (&PortConfig{}).Fields(); len(f) != 0 {
		t.Errorf("empty PortConfig.Fields() = %v, want empty", f)
	}
	if f := (*PortConfig)(nil).Fields(); len(f) != 0 {
		t.Errorf("nil PortConfig.Fields() = %v, want empty", f)
	}

	// Marshal: omitempty keeps unset fields off the wire (mtu is a number).
	if out, _ := json.Marshal(&PortConfig{AdminStatus: "up", MTU: 9100}); string(out) != `{"admin_status":"up","mtu":9100}` {
		t.Errorf("marshal = %s, want {\"admin_status\":\"up\",\"mtu\":9100}", out)
	}
}

// TestPortConfig_SchemaRegistered pins that PortConfig is a real schema kind so
// a universal UI can fetch its config form.
func TestPortConfig_SchemaRegistered(t *testing.T) {
	found := false
	for _, k := range ListSchemaKinds() {
		if k == "PortConfig" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("PortConfig not registered as a schema kind; got %v", ListSchemaKinds())
	}
}

// TestDefaultPortConfig pins the #301 authoring template: one PortConfig per
// platform inventory port, carrying the default admin_status/mtu convention and
// leaving speed unset (inherits the platform default). The result is keyed by
// port name so it drops straight into a TopologyNode's Ports.
func TestDefaultPortConfig(t *testing.T) {
	p := &PlatformSpec{
		Name:  "Force10-S6000",
		HWSKU: "Force10-S6000",
		Ports: []PortSpec{
			{Name: "Ethernet0", NICIndex: 1, Speed: "40G"},
			{Name: "Ethernet4", NICIndex: 2, Speed: "40G"},
		},
	}
	got := DefaultPortConfig(p)
	if len(got) != 2 {
		t.Fatalf("want 2 ports, got %d: %v", len(got), got)
	}
	pc, ok := got["Ethernet0"]
	if !ok {
		t.Fatalf("Ethernet0 missing from %v", got)
	}
	if pc.AdminStatus != "up" || pc.MTU != 9100 {
		t.Errorf("Ethernet0 = %+v, want {admin up, mtu 9100}", pc)
	}
	// Speed is left unset so the port inherits the platform default_speed.
	if pc.Speed != "" {
		t.Errorf("Ethernet0 speed = %q, want unset (inherits platform default)", pc.Speed)
	}
}

// TestDefaultPortConfig_NoInventory: a host / HWSKU-less platform (no Ports) and
// a nil platform both yield a non-nil empty map, so callers can assign it
// unconditionally.
func TestDefaultPortConfig_NoInventory(t *testing.T) {
	if got := DefaultPortConfig(&PlatformSpec{Name: "host", DeviceType: "host"}); got == nil || len(got) != 0 {
		t.Errorf("host platform = %v, want non-nil empty map", got)
	}
	if got := DefaultPortConfig(nil); got == nil || len(got) != 0 {
		t.Errorf("nil platform = %v, want non-nil empty map", got)
	}
}

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
	if got, want := dev.Ports["Ethernet4"].Fields(), (map[string]string{"admin_status": "down", "speed": "100G"}); !reflect.DeepEqual(got, want) {
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

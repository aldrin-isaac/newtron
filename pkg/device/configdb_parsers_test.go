package device

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsers_AllTablesRegistered(t *testing.T) {
	// Every ConfigDB struct field has a JSON tag like `json:"TABLE_NAME,omitempty"`.
	// Verify that tableParsers has an entry for each one.
	typ := reflect.TypeOf(ConfigDB{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		tableName := strings.SplitN(tag, ",", 2)[0]
		if _, ok := tableParsers[tableName]; !ok {
			t.Errorf("ConfigDB field %s (table %q) has no tableParsers entry", field.Name, tableName)
		}
	}
}

func TestParseEntry_RoundTrip(t *testing.T) {
	tests := []struct {
		table string
		entry string
		vals  map[string]string
		check func(t *testing.T, db *ConfigDB)
	}{
		{
			table: "PORT",
			entry: "Ethernet0",
			vals: map[string]string{
				"admin_status": "up",
				"mtu":          "9100",
				"speed":        "100000",
				"fec":          "rs",
				"alias":        "eth0",
				"lanes":        "1,2,3,4",
				"index":        "0",
				"autoneg":      "off",
				"description":  "uplink",
			},
			check: func(t *testing.T, db *ConfigDB) {
				p := db.Port["Ethernet0"]
				if p.AdminStatus != "up" {
					t.Errorf("Port.AdminStatus = %q", p.AdminStatus)
				}
				if p.MTU != "9100" {
					t.Errorf("Port.MTU = %q", p.MTU)
				}
				if p.FEC != "rs" {
					t.Errorf("Port.FEC = %q", p.FEC)
				}
			},
		},
		{
			table: "BGP_NEIGHBOR",
			entry: "10.0.0.2",
			vals: map[string]string{
				"asn":           "65001",
				"local_addr":    "10.0.0.1",
				"name":          "spine1",
				"holdtime":      "180",
				"keepalive":     "60",
				"admin_status":  "up",
				"peer_group":    "SPINE_PEERS",
				"ebgp_multihop": "2",
				"password":      "secret",
			},
			check: func(t *testing.T, db *ConfigDB) {
				n := db.BGPNeighbor["10.0.0.2"]
				if n.ASN != "65001" {
					t.Errorf("BGPNeighbor.ASN = %q", n.ASN)
				}
				if n.PeerGroup != "SPINE_PEERS" {
					t.Errorf("BGPNeighbor.PeerGroup = %q", n.PeerGroup)
				}
				if n.EBGPMultihop != "2" {
					t.Errorf("BGPNeighbor.EBGPMultihop = %q", n.EBGPMultihop)
				}
			},
		},
		{
			table: "ACL_RULE",
			entry: "MY_ACL|RULE_10",
			vals: map[string]string{
				"PRIORITY":      "10",
				"PACKET_ACTION": "FORWARD",
				"SRC_IP":        "10.0.0.0/8",
				"IP_PROTOCOL":   "6",
				"L4_DST_PORT":   "80",
				"TCP_FLAGS":     "0x02/0x02",
				"ETHER_TYPE":    "0x0800",
			},
			check: func(t *testing.T, db *ConfigDB) {
				r := db.ACLRule["MY_ACL|RULE_10"]
				if r.Priority != "10" {
					t.Errorf("ACLRule.Priority = %q", r.Priority)
				}
				if r.PacketAction != "FORWARD" {
					t.Errorf("ACLRule.PacketAction = %q", r.PacketAction)
				}
				if r.TCPFlags != "0x02/0x02" {
					t.Errorf("ACLRule.TCPFlags = %q", r.TCPFlags)
				}
				if r.EtherType != "0x0800" {
					t.Errorf("ACLRule.EtherType = %q", r.EtherType)
				}
			},
		},
		{
			table: "VLAN_INTERFACE",
			entry: "Vlan100",
			vals: map[string]string{
				"vrf_name":  "Vrf_CUST1",
				"proxy_arp": "enabled",
			},
			check: func(t *testing.T, db *ConfigDB) {
				m := db.VLANInterface["Vlan100"]
				if m == nil {
					t.Fatal("VLANInterface[Vlan100] is nil")
				}
				if m["vrf_name"] != "Vrf_CUST1" {
					t.Errorf("VLANInterface vrf_name = %q", m["vrf_name"])
				}
				if m["proxy_arp"] != "enabled" {
					t.Errorf("VLANInterface proxy_arp = %q", m["proxy_arp"])
				}
			},
		},
		{
			table: "ROUTE_TABLE",
			entry: "10.1.0.0/24",
			vals: map[string]string{
				"nexthop":     "10.0.0.1",
				"ifname":      "Ethernet0",
				"distance":    "1",
				"nexthop-vrf": "default",
			},
			check: func(t *testing.T, db *ConfigDB) {
				r := db.RouteTable["10.1.0.0/24"]
				if r.NextHop != "10.0.0.1" {
					t.Errorf("RouteTable.NextHop = %q", r.NextHop)
				}
				if r.Interface != "Ethernet0" {
					t.Errorf("RouteTable.Interface = %q", r.Interface)
				}
				if r.NextHopVRF != "default" {
					t.Errorf("RouteTable.NextHopVRF = %q", r.NextHopVRF)
				}
			},
		},
		{
			table: "SCHEDULER",
			entry: "scheduler.0",
			vals: map[string]string{
				"type":   "DWRR",
				"weight": "14",
			},
			check: func(t *testing.T, db *ConfigDB) {
				s := db.Scheduler["scheduler.0"]
				if s.Type != "DWRR" {
					t.Errorf("Scheduler.Type = %q", s.Type)
				}
				if s.Weight != "14" {
					t.Errorf("Scheduler.Weight = %q", s.Weight)
				}
			},
		},
		{
			table: "DSCP_TO_TC_MAP",
			entry: "AZURE",
			vals: map[string]string{
				"0": "0",
				"8": "1",
			},
			check: func(t *testing.T, db *ConfigDB) {
				m := db.DSCPToTCMap["AZURE"]
				if m == nil {
					t.Fatal("DSCPToTCMap[AZURE] is nil")
				}
				if m["0"] != "0" {
					t.Errorf("DSCPToTCMap[AZURE][0] = %q", m["0"])
				}
				if m["8"] != "1" {
					t.Errorf("DSCPToTCMap[AZURE][8] = %q", m["8"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.table, func(t *testing.T) {
			db := newEmptyConfigDB()
			parser, ok := tableParsers[tt.table]
			if !ok {
				t.Fatalf("no parser for table %q", tt.table)
			}
			parser(db, tt.entry, tt.vals)
			tt.check(t, db)
		})
	}
}

func TestParseEntry_UnknownTable(t *testing.T) {
	db := newEmptyConfigDB()
	// Must not panic on unknown table names.
	client := &ConfigDBClient{}
	client.parseEntry(db, "NONEXISTENT_TABLE", "key1", map[string]string{"foo": "bar"})
}

func TestConfigDB_Has_Positive(t *testing.T) {
	db := newEmptyConfigDB()
	db.VLAN["Vlan100"] = VLANEntry{VLANID: "100"}
	db.VRF["Vrf_CUST1"] = VRFEntry{}
	db.PortChannel["PortChannel100"] = PortChannelEntry{}
	db.ACLTable["MY_ACL"] = ACLTableEntry{Type: "L3"}
	db.VXLANTunnel["vtep1"] = VXLANTunnelEntry{SrcIP: "10.0.0.1"}
	db.BGPNeighbor["default|10.0.0.2"] = BGPNeighborEntry{ASN: "65001"}
	db.Port["Ethernet0"] = PortEntry{}

	if !db.HasVLAN(100) {
		t.Error("HasVLAN(100) = false, want true")
	}
	if !db.HasVRF("Vrf_CUST1") {
		t.Error("HasVRF(Vrf_CUST1) = false, want true")
	}
	if !db.HasPortChannel("PortChannel100") {
		t.Error("HasPortChannel(PortChannel100) = false, want true")
	}
	if !db.HasACLTable("MY_ACL") {
		t.Error("HasACLTable(MY_ACL) = false, want true")
	}
	if !db.HasVTEP() {
		t.Error("HasVTEP() = false, want true")
	}
	if !db.HasBGPNeighbor("default|10.0.0.2") {
		t.Error("HasBGPNeighbor(default|10.0.0.2) = false, want true")
	}
	if !db.HasInterface("Ethernet0") {
		t.Error("HasInterface(Ethernet0) = false, want true")
	}
	if !db.HasInterface("PortChannel100") {
		t.Error("HasInterface(PortChannel100) = false, want true")
	}
}

func TestConfigDB_Has_Negative(t *testing.T) {
	db := newEmptyConfigDB()

	if db.HasVLAN(100) {
		t.Error("HasVLAN(100) = true on empty DB")
	}
	if db.HasVRF("Vrf_CUST1") {
		t.Error("HasVRF = true on empty DB")
	}
	if db.HasPortChannel("PortChannel100") {
		t.Error("HasPortChannel = true on empty DB")
	}
	if db.HasACLTable("MY_ACL") {
		t.Error("HasACLTable = true on empty DB")
	}
	if db.HasVTEP() {
		t.Error("HasVTEP = true on empty DB")
	}
	if db.HasBGPNeighbor("default|10.0.0.2") {
		t.Error("HasBGPNeighbor = true on empty DB")
	}
	if db.HasInterface("Ethernet0") {
		t.Error("HasInterface = true on empty DB")
	}
	if db.BGPConfigured() {
		t.Error("BGPConfigured = true on empty DB")
	}
}

func TestConfigDB_Has_NilReceiver(t *testing.T) {
	var db *ConfigDB
	if db.HasVLAN(100) {
		t.Error("nil.HasVLAN should be false")
	}
	if db.HasVRF("x") {
		t.Error("nil.HasVRF should be false")
	}
	if db.HasPortChannel("x") {
		t.Error("nil.HasPortChannel should be false")
	}
	if db.HasACLTable("x") {
		t.Error("nil.HasACLTable should be false")
	}
	if db.HasVTEP() {
		t.Error("nil.HasVTEP should be false")
	}
	if db.HasBGPNeighbor("x") {
		t.Error("nil.HasBGPNeighbor should be false")
	}
	if db.HasInterface("x") {
		t.Error("nil.HasInterface should be false")
	}
	if db.BGPConfigured() {
		t.Error("nil.BGPConfigured should be false")
	}
}

func TestConfigDB_BGPConfigured(t *testing.T) {
	// Path 1: BGPNeighbor non-empty
	db := newEmptyConfigDB()
	db.BGPNeighbor["default|10.0.0.2"] = BGPNeighborEntry{ASN: "65001"}
	if !db.BGPConfigured() {
		t.Error("BGPConfigured should be true with BGP neighbor")
	}

	// Path 2: DeviceMetadata bgp_asn
	db2 := newEmptyConfigDB()
	db2.DeviceMetadata["localhost"] = map[string]string{"bgp_asn": "65000"}
	if !db2.BGPConfigured() {
		t.Error("BGPConfigured should be true with device metadata ASN")
	}

	// Path 3: DeviceMetadata with empty bgp_asn
	db3 := newEmptyConfigDB()
	db3.DeviceMetadata["localhost"] = map[string]string{"bgp_asn": ""}
	if db3.BGPConfigured() {
		t.Error("BGPConfigured should be false with empty ASN")
	}
}

func TestNewEmptyConfigDB(t *testing.T) {
	db := newEmptyConfigDB()
	typ := reflect.TypeOf(*db)
	val := reflect.ValueOf(*db)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fv := val.Field(i)
		if fv.Kind() == reflect.Map && fv.IsNil() {
			t.Errorf("ConfigDB.%s is nil, want initialized map", field.Name)
		}
	}
}

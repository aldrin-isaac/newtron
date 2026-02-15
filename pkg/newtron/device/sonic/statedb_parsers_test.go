package sonic

import (
	"reflect"
	"strings"
	"testing"
)

func TestNewEmptyStateDB(t *testing.T) {
	db := newEmptyStateDB()
	typ := reflect.TypeOf(*db)
	val := reflect.ValueOf(*db)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fv := val.Field(i)
		if fv.Kind() == reflect.Map && fv.IsNil() {
			t.Errorf("StateDB.%s is nil, want initialized map", field.Name)
		}
	}
}

func TestStateParsers_AllTablesRegistered(t *testing.T) {
	typ := reflect.TypeOf(StateDB{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		tableName := strings.SplitN(tag, ",", 2)[0]
		if _, ok := stateTableParsers[tableName]; !ok {
			t.Errorf("StateDB field %s (table %q) has no stateTableParsers entry", field.Name, tableName)
		}
	}
}

func TestStateDB_ParseEntry(t *testing.T) {
	tests := []struct {
		table string
		entry string
		vals  map[string]string
		check func(t *testing.T, db *StateDB)
	}{
		{
			table: "PORT_TABLE",
			entry: "Ethernet0",
			vals: map[string]string{
				"admin_status":  "up",
				"oper_status":   "up",
				"speed":         "100000",
				"mtu":           "9100",
				"link_training": "off",
			},
			check: func(t *testing.T, db *StateDB) {
				p := db.PortTable["Ethernet0"]
				if p.AdminStatus != "up" {
					t.Errorf("AdminStatus = %q", p.AdminStatus)
				}
				if p.OperStatus != "up" {
					t.Errorf("OperStatus = %q", p.OperStatus)
				}
				if p.MTU != "9100" {
					t.Errorf("MTU = %q", p.MTU)
				}
			},
		},
		{
			table: "LAG_TABLE",
			entry: "PortChannel100",
			vals: map[string]string{
				"oper_status": "up",
				"speed":       "200000",
				"mtu":         "9100",
			},
			check: func(t *testing.T, db *StateDB) {
				l := db.LAGTable["PortChannel100"]
				if l.OperStatus != "up" {
					t.Errorf("OperStatus = %q", l.OperStatus)
				}
			},
		},
		{
			table: "BGP_NEIGHBOR_TABLE",
			entry: "default|10.0.0.2",
			vals: map[string]string{
				"state":      "Established",
				"remote_asn": "65001",
				"local_asn":  "65000",
				"uptime":     "01:23:45",
			},
			check: func(t *testing.T, db *StateDB) {
				n := db.BGPNeighborTable["default|10.0.0.2"]
				if n.State != "Established" {
					t.Errorf("State = %q", n.State)
				}
				if n.RemoteAS != "65001" {
					t.Errorf("RemoteAS = %q", n.RemoteAS)
				}
			},
		},
		{
			table: "NEIGH_STATE_TABLE",
			entry: "default|10.0.0.3",
			vals: map[string]string{
				"state":      "Active",
				"remote_asn": "65002",
			},
			check: func(t *testing.T, db *StateDB) {
				n := db.BGPNeighborTable["default|10.0.0.3"]
				if n.State != "Active" {
					t.Errorf("State = %q", n.State)
				}
			},
		},
		{
			table: "FDB_TABLE",
			entry: "Vlan100|aa:bb:cc:dd:ee:ff",
			vals: map[string]string{
				"port":        "Ethernet0",
				"type":        "dynamic",
				"vni":         "10100",
				"remote_vtep": "10.0.0.2",
			},
			check: func(t *testing.T, db *StateDB) {
				f := db.FDBTable["Vlan100|aa:bb:cc:dd:ee:ff"]
				if f.Port != "Ethernet0" {
					t.Errorf("Port = %q", f.Port)
				}
				if f.VNI != "10100" {
					t.Errorf("VNI = %q", f.VNI)
				}
			},
		},
		{
			table: "TRANSCEIVER_INFO",
			entry: "Ethernet0",
			vals: map[string]string{
				"vendor_name": "Finisar",
				"model":       "FTLX8571D3BCL",
				"serial_num":  "ABC123",
			},
			check: func(t *testing.T, db *StateDB) {
				ti := db.TransceiverInfo["Ethernet0"]
				if ti.Vendor != "Finisar" {
					t.Errorf("Vendor = %q", ti.Vendor)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.table, func(t *testing.T) {
			db := newEmptyStateDB()
			parser, ok := stateTableParsers[tt.table]
			if !ok {
				t.Fatalf("no parser for table %q", tt.table)
			}
			parser(db, tt.entry, tt.vals)
			tt.check(t, db)
		})
	}
}

func TestStateDB_ParseEntry_UnknownTable(t *testing.T) {
	// Must not panic on unknown table names
	db := newEmptyStateDB()
	if parser, ok := stateTableParsers["NONEXISTENT_TABLE"]; ok {
		parser(db, "key1", map[string]string{"foo": "bar"})
		t.Error("should not have parser for NONEXISTENT_TABLE")
	}
	_ = db // Ensure db is usable
}

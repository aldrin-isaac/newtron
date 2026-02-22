package sonic

import "reflect"

// stateTableParser populates a single StateDB field from a Redis hash entry.
type stateTableParser func(db *StateDB, entry string, vals map[string]string)

// stateTableParsers maps SONiC STATE_DB table names to their parser functions.
var stateTableParsers map[string]stateTableParser

func init() {
	stateTableParsers = map[string]stateTableParser{
		"PORT_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.PortTable[entry] = PortStateEntry{
				AdminStatus:  vals["admin_status"],
				OperStatus:   vals["oper_status"],
				Speed:        vals["speed"],
				MTU:          vals["mtu"],
				LinkTraining: vals["link_training"],
			}
		},
		"LAG_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.LAGTable[entry] = LAGStateEntry{
				OperStatus: vals["oper_status"],
				Speed:      vals["speed"],
				MTU:        vals["mtu"],
			}
		},
		"LAG_MEMBER_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.LAGMemberTable[entry] = LAGMemberStateEntry{
				OperStatus:     vals["oper_status"],
				CollectingDist: vals["collecting_distributing"],
				Selected:       vals["selected"],
				ActorPortNum:   vals["actor_port_num"],
				PartnerPortNum: vals["partner_port_num"],
			}
		},
		"VLAN_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.VLANTable[entry] = VLANStateEntry{
				OperStatus: vals["oper_status"],
				State:      vals["state"],
			}
		},
		"VRF_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.VRFTable[entry] = VRFStateEntry{
				State: vals["state"],
			}
		},
		"VXLAN_TUNNEL_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.VXLANTunnelTable[entry] = VXLANTunnelStateEntry{
				SrcIP:      vals["src_ip"],
				OperStatus: vals["operstatus"],
			}
		},
		"BGP_NEIGHBOR_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.BGPNeighborTable[entry] = parseBGPNeighborState(vals)
		},
		"INTERFACE_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.InterfaceTable[entry] = InterfaceStateEntry{
				VRF:      vals["vrf"],
				ProxyArp: vals["proxy_arp"],
			}
		},
		"NEIGH_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.NeighTable[entry] = NeighStateEntry{
				Family: vals["family"],
				MAC:    vals["neigh"],
				State:  vals["state"],
			}
		},
		"FDB_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.FDBTable[entry] = FDBStateEntry{
				Port:       vals["port"],
				Type:       vals["type"],
				VNI:        vals["vni"],
				RemoteVTEP: vals["remote_vtep"],
			}
		},
		"ROUTE_TABLE": func(db *StateDB, entry string, vals map[string]string) {
			db.RouteTable[entry] = RouteStateEntry{
				NextHop:   vals["nexthop"],
				Interface: vals["ifname"],
				Protocol:  vals["protocol"],
			}
		},
		"TRANSCEIVER_INFO": func(db *StateDB, entry string, vals map[string]string) {
			db.TransceiverInfo[entry] = TransceiverInfoEntry{
				Vendor:          vals["vendor_name"],
				Model:           vals["model"],
				SerialNum:       vals["serial_num"],
				HardwareVersion: vals["hardware_version"],
				Type:            vals["type"],
				MediaInterface:  vals["media_interface"],
			}
		},
		"TRANSCEIVER_STATUS": func(db *StateDB, entry string, vals map[string]string) {
			db.TransceiverStatus[entry] = TransceiverStatusEntry{
				Present:     vals["present"],
				Temperature: vals["temperature"],
				Voltage:     vals["voltage"],
				TxPower:     vals["tx_power"],
				RxPower:     vals["rx_power"],
			}
		},
	}
}

func parseBGPNeighborState(vals map[string]string) BGPNeighborStateEntry {
	return BGPNeighborStateEntry{
		State:           vals["state"],
		RemoteAS:        vals["remote_asn"],
		LocalAS:         vals["local_asn"],
		PeerGroup:       vals["peer_group"],
		PfxRcvd:         vals["prefixes_received"],
		PfxSent:         vals["prefixes_sent"],
		MsgRcvd:         vals["msg_rcvd"],
		MsgSent:         vals["msg_sent"],
		Uptime:          vals["uptime"],
		HoldTime:        vals["holdtime"],
		KeepaliveTime:   vals["keepalive"],
		ConnectRetry:    vals["connect_retry"],
		LastResetReason: vals["last_reset_reason"],
	}
}

// newEmptyStateDB returns a StateDB with all map fields initialized.
func newEmptyStateDB() *StateDB {
	db := &StateDB{}
	initMaps(reflect.ValueOf(db).Elem())
	return db
}

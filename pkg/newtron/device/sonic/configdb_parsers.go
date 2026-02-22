package sonic

import "reflect"

// tableParser populates a single ConfigDB field from a Redis hash entry.
type tableParser func(db *ConfigDB, entry string, vals map[string]string)

// tableParsers maps SONiC CONFIG_DB table names to their parser functions.
// Every ConfigDB struct field must have a corresponding entry here.
var tableParsers map[string]tableParser

func init() {
	tableParsers = map[string]tableParser{
		// ---- Typed struct parsers (33 tables) ----

		"PORT": func(db *ConfigDB, entry string, vals map[string]string) {
			db.Port[entry] = PortEntry{
				AdminStatus: vals["admin_status"],
				Alias:       vals["alias"],
				Description: vals["description"],
				FEC:         vals["fec"],
				Index:       vals["index"],
				Lanes:       vals["lanes"],
				MTU:         vals["mtu"],
				Speed:       vals["speed"],
				Autoneg:     vals["autoneg"],
			}
		},
		"VLAN": func(db *ConfigDB, entry string, vals map[string]string) {
			db.VLAN[entry] = VLANEntry{
				VLANID:      vals["vlanid"],
				Description: vals["description"],
				MTU:         vals["mtu"],
				AdminStatus: vals["admin_status"],
				DHCPServers: vals["dhcp_servers"],
			}
		},
		"VLAN_MEMBER": func(db *ConfigDB, entry string, vals map[string]string) {
			db.VLANMember[entry] = VLANMemberEntry{
				TaggingMode: vals["tagging_mode"],
			}
		},
		"INTERFACE": func(db *ConfigDB, entry string, vals map[string]string) {
			db.Interface[entry] = InterfaceEntry{
				VRFName:     vals["vrf_name"],
				NATZone:     vals["nat_zone"],
				ProxyArp:    vals["proxy_arp"],
				MPLSEnabled: vals["mpls"],
			}
		},
		"PORTCHANNEL": func(db *ConfigDB, entry string, vals map[string]string) {
			db.PortChannel[entry] = PortChannelEntry{
				AdminStatus: vals["admin_status"],
				MTU:         vals["mtu"],
				MinLinks:    vals["min_links"],
				Fallback:    vals["fallback"],
				FastRate:    vals["fast_rate"],
				LACPKey:     vals["lacp_key"],
				Description: vals["description"],
			}
		},
		"VRF": func(db *ConfigDB, entry string, vals map[string]string) {
			db.VRF[entry] = VRFEntry{
				VNI:      vals["vni"],
				Fallback: vals["fallback"],
			}
		},
		"VXLAN_TUNNEL": func(db *ConfigDB, entry string, vals map[string]string) {
			db.VXLANTunnel[entry] = VXLANTunnelEntry{
				SrcIP: vals["src_ip"],
			}
		},
		"VXLAN_TUNNEL_MAP": func(db *ConfigDB, entry string, vals map[string]string) {
			db.VXLANTunnelMap[entry] = VXLANMapEntry{
				VLAN: vals["vlan"],
				VRF:  vals["vrf"],
				VNI:  vals["vni"],
			}
		},
		"VXLAN_EVPN_NVO": func(db *ConfigDB, entry string, vals map[string]string) {
			db.VXLANEVPNNVO[entry] = EVPNNVOEntry{
				SourceVTEP: vals["source_vtep"],
			}
		},
		"BGP_NEIGHBOR": func(db *ConfigDB, entry string, vals map[string]string) {
			db.BGPNeighbor[entry] = BGPNeighborEntry{
				LocalAddr:     vals["local_addr"],
				Name:          vals["name"],
				ASN:           vals["asn"],
				HoldTime:      vals["holdtime"],
				KeepaliveTime: vals["keepalive"],
				AdminStatus:   vals["admin_status"],
				PeerGroup:     vals["peer_group"],
				EBGPMultihop:  vals["ebgp_multihop"],
				Password:      vals["password"],
			}
		},
		"BGP_NEIGHBOR_AF": func(db *ConfigDB, entry string, vals map[string]string) {
			db.BGPNeighborAF[entry] = BGPNeighborAFEntry{
				Activate:             vals["activate"],
				RouteReflectorClient: vals["route_reflector_client"],
				NextHopSelf:          vals["next_hop_self"],
				SoftReconfiguration:  vals["soft_reconfiguration"],
				AllowASIn:            vals["allowas_in"],
				RouteMapIn:           vals["route_map_in"],
				RouteMapOut:          vals["route_map_out"],
				PrefixListIn:         vals["prefix_list_in"],
				PrefixListOut:        vals["prefix_list_out"],
				DefaultOriginate:     vals["default_originate"],
				AddpathTxAll:         vals["addpath_tx_all_paths"],
			}
		},
		"BGP_GLOBALS": func(db *ConfigDB, entry string, vals map[string]string) {
			db.BGPGlobals[entry] = BGPGlobalsEntry{
				RouterID:            vals["router_id"],
				LocalASN:            vals["local_asn"],
				ConfedID:            vals["confed_id"],
				ConfedPeers:         vals["confed_peers"],
				GracefulRestart:     vals["graceful_restart"],
				LoadBalanceMPRelax:  vals["load_balance_mp_relax"],
				RRClusterID:         vals["rr_cluster_id"],
				EBGPRequiresPolicy:  vals["ebgp_requires_policy"],
				DefaultIPv4Unicast:  vals["default_ipv4_unicast"],
				LogNeighborChanges:  vals["log_neighbor_changes"],
				SuppressFIBPending:  vals["suppress_fib_pending"],
			}
		},
		"BGP_GLOBALS_AF": func(db *ConfigDB, entry string, vals map[string]string) {
			db.BGPGlobalsAF[entry] = BGPGlobalsAFEntry{
				AdvertiseAllVNI:    vals["advertise-all-vni"],
				AdvertiseDefaultGW: vals["advertise-default-gw"],
				AdvertiseSVIIP:     vals["advertise-svi-ip"],
				AdvertiseIPv4:      vals["advertise_ipv4_unicast"],
				AdvertiseIPv6:      vals["advertise_ipv6_unicast"],
				RD:                 vals["rd"],
				RTImport:           vals["rt_import"],
				RTExport:           vals["rt_export"],
				RTImportEVPN:       vals["route_target_import_evpn"],
				RTExportEVPN:       vals["route_target_export_evpn"],
				MaxEBGPPaths:       vals["max_ebgp_paths"],
				MaxIBGPPaths:       vals["max_ibgp_paths"],
			}
		},
		"BGP_EVPN_VNI": func(db *ConfigDB, entry string, vals map[string]string) {
			db.BGPEVPNVNI[entry] = BGPEVPNVNIEntry{
				RD:                 vals["rd"],
				RTImport:           vals["route_target_import"],
				RTExport:           vals["route_target_export"],
				AdvertiseDefaultGW: vals["advertise_default_gw"],
			}
		},
		"BGP_GLOBALS_EVPN_RT": func(db *ConfigDB, entry string, vals map[string]string) {
			db.BGPGlobalsEVPNRT[entry] = BGPGlobalsEVPNRTEntry{
				RouteTargetType: vals["route-target-type"],
			}
		},
		"ROUTE_TABLE": func(db *ConfigDB, entry string, vals map[string]string) {
			db.RouteTable[entry] = StaticRouteEntry{
				NextHop:    vals["nexthop"],
				Interface:  vals["ifname"],
				Distance:   vals["distance"],
				NextHopVRF: vals["nexthop-vrf"],
				Blackhole:  vals["blackhole"],
			}
		},
		"ACL_TABLE": func(db *ConfigDB, entry string, vals map[string]string) {
			db.ACLTable[entry] = ACLTableEntry{
				PolicyDesc: vals["policy_desc"],
				Type:       vals["type"],
				Stage:      vals["stage"],
				Ports:      vals["ports"],
				Services:   vals["services"],
			}
		},
		"ACL_RULE": func(db *ConfigDB, entry string, vals map[string]string) {
			db.ACLRule[entry] = ACLRuleEntry{
				Priority:       vals["PRIORITY"],
				PacketAction:   vals["PACKET_ACTION"],
				SrcIP:          vals["SRC_IP"],
				DstIP:          vals["DST_IP"],
				IPProtocol:     vals["IP_PROTOCOL"],
				L4SrcPort:      vals["L4_SRC_PORT"],
				L4DstPort:      vals["L4_DST_PORT"],
				L4SrcPortRange: vals["L4_SRC_PORT_RANGE"],
				L4DstPortRange: vals["L4_DST_PORT_RANGE"],
				TCPFlags:       vals["TCP_FLAGS"],
				DSCP:           vals["DSCP"],
				ICMPType:       vals["ICMP_TYPE"],
				ICMPCode:       vals["ICMP_CODE"],
				EtherType:      vals["ETHER_TYPE"],
				InPorts:        vals["IN_PORTS"],
				RedirectPort:   vals["REDIRECT_PORT"],
			}
		},
		"ACL_TABLE_TYPE": func(db *ConfigDB, entry string, vals map[string]string) {
			db.ACLTableType[entry] = ACLTableTypeEntry{
				MatchFields:   vals["matches"],
				Actions:       vals["actions"],
				BindPointType: vals["bind_point_type"],
			}
		},
		"SCHEDULER": func(db *ConfigDB, entry string, vals map[string]string) {
			db.Scheduler[entry] = SchedulerEntry{
				Type:   vals["type"],
				Weight: vals["weight"],
			}
		},
		"QUEUE": func(db *ConfigDB, entry string, vals map[string]string) {
			db.Queue[entry] = QueueEntry{
				Scheduler:   vals["scheduler"],
				WREDProfile: vals["wred_profile"],
			}
		},
		"WRED_PROFILE": func(db *ConfigDB, entry string, vals map[string]string) {
			db.WREDProfile[entry] = WREDProfileEntry{
				GreenMinThreshold:     vals["green_min_threshold"],
				GreenMaxThreshold:     vals["green_max_threshold"],
				GreenDropProbability:  vals["green_drop_probability"],
				YellowMinThreshold:    vals["yellow_min_threshold"],
				YellowMaxThreshold:    vals["yellow_max_threshold"],
				YellowDropProbability: vals["yellow_drop_probability"],
				RedMinThreshold:       vals["red_min_threshold"],
				RedMaxThreshold:       vals["red_max_threshold"],
				RedDropProbability:    vals["red_drop_probability"],
				ECN:                   vals["ecn"],
			}
		},
		"PORT_QOS_MAP": func(db *ConfigDB, entry string, vals map[string]string) {
			db.PortQoSMap[entry] = PortQoSMapEntry{
				DSCPToTCMap:  vals["dscp_to_tc_map"],
				TCToQueueMap: vals["tc_to_queue_map"],
			}
		},
		"NEWTRON_SERVICE_BINDING": func(db *ConfigDB, entry string, vals map[string]string) {
			db.NewtronServiceBinding[entry] = ServiceBindingEntry{
				ServiceName: vals["service_name"],
				IPAddress:   vals["ip_address"],
				VRFName:     vals["vrf_name"],
				IPVPN:       vals["ipvpn"],
				MACVPN:      vals["macvpn"],
				IngressACL:  vals["ingress_acl"],
				EgressACL:   vals["egress_acl"],
				BGPNeighbor: vals["bgp_neighbor"],
				AppliedAt:   vals["applied_at"],
				AppliedBy:   vals["applied_by"],
			}
		},
		"ROUTE_REDISTRIBUTE": func(db *ConfigDB, entry string, vals map[string]string) {
			db.RouteRedistribute[entry] = RouteRedistributeEntry{
				RouteMap: vals["route_map"],
				Metric:   vals["metric"],
			}
		},
		"ROUTE_MAP": func(db *ConfigDB, entry string, vals map[string]string) {
			db.RouteMap[entry] = RouteMapEntry{
				Action:         vals["route_operation"],
				MatchPrefixSet: vals["match_prefix_set"],
				MatchCommunity: vals["match_community"],
				MatchASPath:    vals["match_as_path"],
				MatchNextHop:   vals["match_next_hop"],
				SetLocalPref:   vals["set_local_pref"],
				SetCommunity:   vals["set_community"],
				SetMED:         vals["set_med"],
				SetNextHop:     vals["set_next_hop"],
			}
		},
		"BGP_PEER_GROUP": func(db *ConfigDB, entry string, vals map[string]string) {
			db.BGPPeerGroup[entry] = BGPPeerGroupEntry{
				ASN:         vals["asn"],
				LocalAddr:   vals["local_addr"],
				AdminStatus: vals["admin_status"],
				HoldTime:    vals["holdtime"],
				Keepalive:   vals["keepalive"],
				Password:    vals["password"],
			}
		},
		"BGP_PEER_GROUP_AF": func(db *ConfigDB, entry string, vals map[string]string) {
			db.BGPPeerGroupAF[entry] = BGPPeerGroupAFEntry{
				Activate:             vals["activate"],
				RouteReflectorClient: vals["route_reflector_client"],
				NextHopSelf:          vals["next_hop_self"],
				RouteMapIn:           vals["route_map_in"],
				RouteMapOut:          vals["route_map_out"],
				SoftReconfiguration:  vals["soft_reconfiguration"],
			}
		},
		"BGP_GLOBALS_AF_NETWORK": func(db *ConfigDB, entry string, vals map[string]string) {
			db.BGPGlobalsAFNet[entry] = BGPGlobalsAFNetEntry{
				Policy: vals["policy"],
			}
		},
		"BGP_GLOBALS_AF_AGGREGATE_ADDR": func(db *ConfigDB, entry string, vals map[string]string) {
			db.BGPGlobalsAFAgg[entry] = BGPGlobalsAFAggEntry{
				AsSet:       vals["as_set"],
				SummaryOnly: vals["summary_only"],
			}
		},
		"PREFIX_SET": func(db *ConfigDB, entry string, vals map[string]string) {
			db.PrefixSet[entry] = PrefixSetEntry{
				IPPrefix:     vals["ip_prefix"],
				Action:       vals["action"],
				MaskLenRange: vals["masklength_range"],
			}
		},
		"COMMUNITY_SET": func(db *ConfigDB, entry string, vals map[string]string) {
			db.CommunitySet[entry] = CommunitySetEntry{
				SetType:         vals["set_type"],
				MatchAction:     vals["match_action"],
				CommunityMember: vals["community_member"],
			}
		},
		"AS_PATH_SET": func(db *ConfigDB, entry string, vals map[string]string) {
			db.ASPathSet[entry] = ASPathSetEntry{
				ASPathMember: vals["as_path_member"],
			}
		},
		// ---- Hash-merge parsers (9 tables) ----

		"DEVICE_METADATA":     mergeParser(func(db *ConfigDB) map[string]map[string]string { return db.DeviceMetadata }),
		"VLAN_INTERFACE":      mergeParser(func(db *ConfigDB) map[string]map[string]string { return db.VLANInterface }),
		"LOOPBACK_INTERFACE":  mergeParser(func(db *ConfigDB) map[string]map[string]string { return db.LoopbackInterface }),
		"PORTCHANNEL_MEMBER":  mergeParser(func(db *ConfigDB) map[string]map[string]string { return db.PortChannelMember }),
		"SUPPRESS_VLAN_NEIGH": mergeParser(func(db *ConfigDB) map[string]map[string]string { return db.SuppressVLANNeigh }),
		"SAG":                 mergeParser(func(db *ConfigDB) map[string]map[string]string { return db.SAG }),
		"SAG_GLOBAL":          mergeParser(func(db *ConfigDB) map[string]map[string]string { return db.SAGGlobal }),
		"DSCP_TO_TC_MAP":      mergeParser(func(db *ConfigDB) map[string]map[string]string { return db.DSCPToTCMap }),
		"TC_TO_QUEUE_MAP":     mergeParser(func(db *ConfigDB) map[string]map[string]string { return db.TCToQueueMap }),
	}
}

// mergeParser returns a tableParser that copies all key-value pairs into a
// nested map[string]map[string]string. Used for tables whose entries have
// variable/unknown field names (DEVICE_METADATA, VLAN_INTERFACE, etc.).
func mergeParser(getMap func(*ConfigDB) map[string]map[string]string) tableParser {
	return func(db *ConfigDB, entry string, vals map[string]string) {
		m := getMap(db)
		if m[entry] == nil {
			m[entry] = make(map[string]string)
		}
		for k, v := range vals {
			m[entry][k] = v
		}
	}
}

// newEmptyConfigDB returns a ConfigDB with all map fields initialized.
func newEmptyConfigDB() *ConfigDB {
	db := &ConfigDB{}
	initMaps(reflect.ValueOf(db).Elem())
	return db
}

// initMaps initializes all nil map fields on a struct using reflection.
func initMaps(v reflect.Value) {
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() == reflect.Map && f.IsNil() {
			f.Set(reflect.MakeMap(f.Type()))
		}
	}
}

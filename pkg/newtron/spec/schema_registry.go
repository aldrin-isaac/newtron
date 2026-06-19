package spec

// init registers every spec kind that the schema metadata endpoint exposes.
// Each Register call provides:
//   - a stable kind name (the Go type name; used in /schema/{kind} URLs and
//     in `ref:"..."` tags on other types)
//   - a human label for the kind itself (rendered as the form title)
//   - an optional description (the kind's tooltip)
//   - a zero value of the type, used by reflection to read field tags
//
// The kind names here ARE the canonical strings every UI uses. Renaming a
// kind ripples to every `ref:"..."` tag that points at it — by design, so
// the rename surfaces immediately.
func init() {
	RegisterSchemaKind(
		"ServiceSpec",
		"Service",
		"A reusable template that binds VPN references, routing, filters, and QoS — applied to interfaces.",
		ServiceSpec{},
	)
	RegisterSchemaKind(
		"RoutingSpec",
		"Service Routing",
		"BGP / static routing parameters for a service.",
		RoutingSpec{},
	)
	RegisterSchemaKind(
		"IPVPNSpec",
		"IP-VPN",
		"A Layer-3 VPN — a VRF + L3VNI + route targets that L3 services attach to.",
		IPVPNSpec{},
	)
	RegisterSchemaKind(
		"MACVPNSpec",
		"MAC-VPN",
		"A Layer-2 VPN — a VLAN + L2VNI + (optional) anycast gateway that L2 services attach to.",
		MACVPNSpec{},
	)
	RegisterSchemaKind(
		"QoSPolicy",
		"QoS Policy",
		"A declarative queue policy — strict / DWRR scheduling, DSCP mapping, optional ECN.",
		QoSPolicy{},
	)
	RegisterSchemaKind(
		"QoSQueue",
		"QoS Queue",
		"One queue (traffic class) within a QoS policy.",
		QoSQueue{},
	)
	RegisterSchemaKind(
		"FilterSpec",
		"Filter",
		"A reusable ACL — an ordered list of permit/deny rules.",
		FilterSpec{},
	)
	RegisterSchemaKind(
		"FilterRule",
		"Filter Rule",
		"One match-and-action rule inside a filter.",
		FilterRule{},
	)
	RegisterSchemaKind(
		"RoutePolicy",
		"Route Policy",
		"A reusable BGP route-policy — an ordered list of match-and-set rules.",
		RoutePolicy{},
	)
	RegisterSchemaKind(
		"RoutePolicyRule",
		"Route Policy Rule",
		"One match-and-action rule inside a route policy.",
		RoutePolicyRule{},
	)
	RegisterSchemaKind(
		"RoutePolicySet",
		"Route Policy Set Actions",
		"Attributes (LOCAL_PREF, community, MED) applied to permitted routes.",
		RoutePolicySet{},
	)
	RegisterSchemaKind(
		"DeviceProfile",
		"Device Profile",
		"Per-device specification — management IP, loopback IP, zone, optional EVPN peering, and per-device overrides.",
		DeviceProfile{},
	)
	RegisterSchemaKind(
		"EVPNConfig",
		"EVPN Overlay Peering",
		"Per-device EVPN BGP overlay peering — peers, route-reflector status, cluster ID.",
		EVPNConfig{},
	)
	RegisterSchemaKind(
		"ZoneSpec",
		"Zone",
		"A zone — a scope for spec overrides between network-global and per-device.",
		ZoneSpec{},
	)
}

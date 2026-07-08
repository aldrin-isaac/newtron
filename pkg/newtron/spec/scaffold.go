package spec

import "strconv"

// setup-device bring-up constants — the frrcfgd/bgpcfgd invariants every managed
// node needs before any service can land on it.
//
// setupDeviceRole is written to DEVICE_METADATA["localhost"]["type"]. It is a
// constant "LeafRouter" for every node: SONiC's device type is a bgpcfgd
// presence-dependency (without it bgpcfgd defers every BGP_NEIGHBOR), not a
// fabric role newtron branches on — configureBGP (bgp_ops.go) writes the same
// value for leaf and spine alike, and newtron derives route-reflector behavior
// from EVPN.RouteReflector, never from this field. So it is not an operator
// choice and does not appear in any authoring schema.
const (
	setupDeviceRole              = "LeafRouter"
	setupDeviceRoutingConfigMode = "unified"
	setupDeviceFRRMgmtFramework  = "true"
)

// ScaffoldTopologyNode builds the topology placement for a freshly-created node:
// a single /setup-device bring-up step derived from the node definition. This is
// the SINGLE OWNER (§27) of the node-definition → setup-device derivation — the
// shape that was composed client-side (by newtcon) before topology membership
// began following the node definition. CreateNodeSpec calls it so a node is
// provision-ready the moment it is defined, with no second authoring step.
//
// hwsku is the resolved platform HWSKU, or "" when the node has no platform (or
// the platform declares no HWSKU) — in which case the field is omitted and
// setup-device infers a default.
//
// EVPN fabric peering — the setup-device source_ip (VTEP source) and
// route_reflector params — is deliberately NOT scaffolded here: a route
// reflector's clients are other nodes, resolvable only against the whole fabric,
// so that derivation is network-wide and authored separately. What create-node
// owns is the node-local bring-up.
func ScaffoldTopologyNode(name string, nodeSpec *NodeSpec, hwsku string, isHost bool) *TopologyNode {
	// Hosts are Linux VMs, not SONiC devices — /setup-device (DEVICE_METADATA,
	// LeafRouter role, frr config mode) is meaningless and provisioning against
	// them fails. A host's placement is bare: it exists in the topology so
	// links can reference it; newtlab wires and provisions it at deploy.
	if isHost {
		return &TopologyNode{}
	}
	fields := map[string]any{
		"hostname":                   name,
		"type":                       setupDeviceRole,
		"docker_routing_config_mode": setupDeviceRoutingConfigMode,
		"frr_mgmt_framework_config":  setupDeviceFRRMgmtFramework,
	}
	if hwsku != "" {
		fields["hwsku"] = hwsku
	}
	if nodeSpec != nil && nodeSpec.UnderlayASN != 0 {
		fields["bgp_asn"] = strconv.Itoa(nodeSpec.UnderlayASN)
	}
	return &TopologyNode{
		Steps: []TopologyStep{{
			URL:    "/setup-device",
			Params: map[string]any{"fields": fields},
		}},
	}
}

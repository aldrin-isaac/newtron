package node

import (
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
)

// VNIMapKey returns the CONFIG_DB key for a VXLAN_TUNNEL_MAP entry.
// Target is a VLAN name (e.g., "Vlan100") or VRF name.
func VNIMapKey(vni int, target string) string {
	return fmt.Sprintf("vtep1|VNI%d_%s", vni, target)
}

// BGPEVPNVNIKey returns the CONFIG_DB key for a BGP_EVPN_VNI entry.
func BGPEVPNVNIKey(vrfName string, vni int) string {
	return fmt.Sprintf("%s|%d", vrfName, vni)
}

// CreateVTEPConfig returns the VXLAN_TUNNEL + VXLAN_EVPN_NVO entries for a VTEP.
func CreateVTEPConfig(sourceIP string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "VXLAN_TUNNEL", Key: "vtep1", Fields: map[string]string{"src_ip": sourceIP}},
		{Table: "VXLAN_EVPN_NVO", Key: "nvo1", Fields: map[string]string{"source_vtep": "vtep1"}},
	}
}

// createVniMapConfig returns the VXLAN_TUNNEL_MAP entry that maps a VLAN to an L2VNI.
// vlanName is the SONiC VLAN name (e.g., "Vlan100"); callers with an integer
// should pass VLANName(vlanID).
func createVniMapConfig(vlanName string, vni int) []sonic.Entry {
	return []sonic.Entry{
		{Table: "VXLAN_TUNNEL_MAP", Key: VNIMapKey(vni, vlanName), Fields: map[string]string{
			"vlan": vlanName,
			"vni":  fmt.Sprintf("%d", vni),
		}},
	}
}

// enableArpSuppressionConfig returns the SUPPRESS_VLAN_NEIGH entry for a VLAN.
// vlanName is the SONiC VLAN name (e.g., "Vlan100"); callers with an integer
// should pass VLANName(vlanID).
func enableArpSuppressionConfig(vlanName string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "SUPPRESS_VLAN_NEIGH", Key: vlanName, Fields: map[string]string{
			"suppress": "on",
		}},
	}
}

// disableArpSuppressionConfig returns the delete entry for ARP suppression on a VLAN.
func disableArpSuppressionConfig(vlanName string) []sonic.Entry {
	return []sonic.Entry{{Table: "SUPPRESS_VLAN_NEIGH", Key: vlanName}}
}

// deleteVniMapConfig returns the delete entry for a specific VXLAN_TUNNEL_MAP entry.
func deleteVniMapConfig(vni int, target string) []sonic.Entry {
	return []sonic.Entry{{Table: "VXLAN_TUNNEL_MAP", Key: VNIMapKey(vni, target)}}
}

// deleteVniMapByKeyConfig returns the delete entry for a VXLAN_TUNNEL_MAP given a raw key.
// Used by cleanup paths that iterate configDB and already have the key.
func deleteVniMapByKeyConfig(key string) []sonic.Entry {
	return []sonic.Entry{{Table: "VXLAN_TUNNEL_MAP", Key: key}}
}

// deleteBgpEvpnVNIConfig returns the delete entry for a BGP_EVPN_VNI entry.
func deleteBgpEvpnVNIConfig(vrfName string, vni int) []sonic.Entry {
	return []sonic.Entry{{Table: "BGP_EVPN_VNI", Key: BGPEVPNVNIKey(vrfName, vni)}}
}

// deleteVxlanTunnelConfig returns delete entries for the VXLAN tunnel infrastructure
// (NVO before tunnel — children before parents).
func deleteVxlanTunnelConfig() []sonic.Entry {
	return []sonic.Entry{
		{Table: "VXLAN_EVPN_NVO", Key: "nvo1"},
		{Table: "VXLAN_TUNNEL", Key: "vtep1"},
	}
}

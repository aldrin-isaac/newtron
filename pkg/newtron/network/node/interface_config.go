package node

import (
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
)

// ============================================================================
// Interface Config Functions (pure, no Node/Interface state)
// ============================================================================

// bindVrfConfig returns the INTERFACE entry for binding an interface to a VRF.
// Always includes the vrf_name field: pass "" to clear the VRF binding.
func bindVrfConfig(intfName, vrfName string) []sonic.Entry {
	return []sonic.Entry{{Table: "INTERFACE", Key: intfName,
		Fields: map[string]string{"vrf_name": vrfName}}}
}

// enableIpRoutingConfig returns the base INTERFACE entry that enables IP routing on an interface.
// No VRF binding — just empty fields so SONiC intfmgrd creates the routing entry.
func enableIpRoutingConfig(intfName string) []sonic.Entry {
	return []sonic.Entry{{Table: "INTERFACE", Key: intfName,
		Fields: map[string]string{}}}
}

// assignIpAddressConfig returns the INTERFACE entry for assigning an IP address.
func assignIpAddressConfig(intfName, ipAddr string) []sonic.Entry {
	return []sonic.Entry{{Table: "INTERFACE",
		Key: fmt.Sprintf("%s|%s", intfName, ipAddr), Fields: map[string]string{}}}
}

// deleteInterfaceIPConfig returns a delete entry for an interface IP sub-entry.
func deleteInterfaceIPConfig(intfName, ipAddr string) []sonic.Entry {
	return []sonic.Entry{{Table: "INTERFACE", Key: fmt.Sprintf("%s|%s", intfName, ipAddr)}}
}

// deleteInterfaceBaseConfig returns a delete entry for the base INTERFACE entry.
func deleteInterfaceBaseConfig(intfName string) []sonic.Entry {
	return []sonic.Entry{{Table: "INTERFACE", Key: intfName}}
}

// setPropertyConfig returns an update entry for setting a property on a port or PortChannel.
func setPropertyConfig(tableName, intfName string, fields map[string]string) []sonic.Entry {
	return []sonic.Entry{{Table: tableName, Key: intfName, Fields: fields}}
}

// clearPropertyConfig returns an update entry for clearing a property to its default.
func clearPropertyConfig(tableName, intfName, property string) []sonic.Entry {
	var fields map[string]string
	switch property {
	case "mtu":
		fields = map[string]string{"mtu": "9100"}
	case "speed":
		fields = map[string]string{"speed": ""}
	case "admin-status", "admin_status":
		fields = map[string]string{"admin_status": "up"}
	case "description":
		fields = map[string]string{"description": ""}
	}
	return []sonic.Entry{{Table: tableName, Key: intfName, Fields: fields}}
}

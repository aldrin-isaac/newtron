package node

import (
	"fmt"
	"strconv"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
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

// interfaceIPKey returns the CONFIG_DB key for an INTERFACE IP sub-entry.
// Format: {intf}|{ip}. One owner so the assign and delete paths can never
// drift apart (§15 forward/reverse symmetry).
func interfaceIPKey(intfName, ipAddr string) string {
	return fmt.Sprintf("%s|%s", intfName, ipAddr)
}

// assignIpAddressConfig returns the INTERFACE entry for assigning an IP address.
func assignIpAddressConfig(intfName, ipAddr string) []sonic.Entry {
	return []sonic.Entry{{Table: "INTERFACE",
		Key: interfaceIPKey(intfName, ipAddr), Fields: map[string]string{}}}
}

// deleteInterfaceIPConfig returns a delete entry for an interface IP sub-entry.
func deleteInterfaceIPConfig(intfName, ipAddr string) []sonic.Entry {
	return []sonic.Entry{{Table: "INTERFACE", Key: interfaceIPKey(intfName, ipAddr)}}
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
	// mtu/admin_status revert to the shared default port convention (spec is the
	// single owner — the same values DefaultPortConfig authors, so clearing an
	// override never silently changes a port). speed/description clear to empty.
	var fields map[string]string
	switch property {
	case "mtu":
		fields = map[string]string{"mtu": strconv.Itoa(spec.DefaultPortMTU)}
	case "speed":
		fields = map[string]string{"speed": ""}
	case "admin-status", "admin_status":
		fields = map[string]string{"admin_status": spec.DefaultPortAdminStatus}
	case "description":
		fields = map[string]string{"description": ""}
	}
	return []sonic.Entry{{Table: tableName, Key: intfName, Fields: fields}}
}

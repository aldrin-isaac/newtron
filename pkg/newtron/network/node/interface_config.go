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

// l3Table returns the CONFIG_DB table that realizes an interface's L3
// identity — a per-kind fact of SONiC's schema, split across tables:
// INTERFACE keys are leafrefs to PORT only (sonic-interface.yang), LAGs
// use PORTCHANNEL_INTERFACE (sonic-portchannel.yang), SVIs use
// VLAN_INTERFACE (owned by the vlan noun — the generators here never
// produce it; configure-interface refuses KindIRB at the gate). Key-helper
// family, like interfaceIPKey below: one owner so the assign and delete
// paths can never target different tables.
func l3Table(intfName string) string {
	if interfaceKindOf(intfName) == KindPortChannel {
		return "PORTCHANNEL_INTERFACE"
	}
	return "INTERFACE"
}

// propertyTable returns the CONFIG_DB table that owns an interface's port
// properties (admin_status, mtu, ...) — PORT for physical ports,
// PORTCHANNEL for LAGs (sonic-port.yang / sonic-portchannel.yang). Which
// properties exist per kind is the model's propertyApplicability; this is
// only the delivery-side row selection. Same key-helper family as l3Table.
func propertyTable(intfName string) string {
	if interfaceKindOf(intfName) == KindPortChannel {
		return "PORTCHANNEL"
	}
	return "PORT"
}

// bindVrfConfig returns the L3 entry for binding an interface to a VRF.
// Always includes the vrf_name field: pass "" to clear the VRF binding.
func bindVrfConfig(intfName, vrfName string) []sonic.Entry {
	return []sonic.Entry{{Table: l3Table(intfName), Key: intfName,
		Fields: map[string]string{"vrf_name": vrfName}}}
}

// enableIpRoutingConfig returns the base L3 entry that enables IP routing on an interface.
// No VRF binding — just empty fields so SONiC intfmgrd creates the routing entry.
func enableIpRoutingConfig(intfName string) []sonic.Entry {
	return []sonic.Entry{{Table: l3Table(intfName), Key: intfName,
		Fields: map[string]string{}}}
}

// interfaceIPKey returns the CONFIG_DB key for an interface IP sub-entry.
// Format: {intf}|{ip}. One owner so the assign and delete paths can never
// drift apart (§15 forward/reverse symmetry).
func interfaceIPKey(intfName, ipAddr string) string {
	return fmt.Sprintf("%s|%s", intfName, ipAddr)
}

// assignIpAddressConfig returns the L3 entry for assigning an IP address.
func assignIpAddressConfig(intfName, ipAddr string) []sonic.Entry {
	return []sonic.Entry{{Table: l3Table(intfName),
		Key: interfaceIPKey(intfName, ipAddr), Fields: map[string]string{}}}
}

// deleteInterfaceIPConfig returns a delete entry for an interface IP sub-entry.
func deleteInterfaceIPConfig(intfName, ipAddr string) []sonic.Entry {
	return []sonic.Entry{{Table: l3Table(intfName), Key: interfaceIPKey(intfName, ipAddr)}}
}

// deleteInterfaceBaseConfig returns a delete entry for the base L3 entry.
func deleteInterfaceBaseConfig(intfName string) []sonic.Entry {
	return []sonic.Entry{{Table: l3Table(intfName), Key: intfName}}
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

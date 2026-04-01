package node

import (
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
)

// IRBConfig holds configuration options for ConfigureIRB.
type IRBConfig struct {
	VRF        string // VRF to bind the IRB to
	IPAddress  string // IP address with prefix (e.g., "10.1.100.1/24")
	AnycastMAC string // SAG anycast gateway MAC (e.g., "00:00:00:00:01:01")
}

// VLANConfig holds configuration options for CreateVLAN.
type VLANConfig struct {
	Name        string // VLAN name (alias for Description)
	Description string
	L2VNI       int
}

// VLANName returns the SONiC name for a VLAN (e.g., "Vlan100").
func VLANName(vlanID int) string { return fmt.Sprintf("Vlan%d", vlanID) }

// VLANMemberKey returns the CONFIG_DB key for a VLAN_MEMBER entry.
func VLANMemberKey(vlanID int, intfName string) string {
	return fmt.Sprintf("%s|%s", VLANName(vlanID), intfName)
}

// IRBIPKey returns the CONFIG_DB key for a VLAN_INTERFACE IP entry.
func IRBIPKey(vlanID int, ipAddr string) string {
	return fmt.Sprintf("%s|%s", VLANName(vlanID), ipAddr)
}

// vlanResource returns the canonical resource name for a VLAN (precondition locking).
func vlanResource(id int) string { return VLANName(id) }

// createVlanConfig returns CONFIG_DB entries for a VLAN: a VLAN entry and an optional
// VXLAN_TUNNEL_MAP entry when L2VNI is specified.
func createVlanConfig(vlanID int, opts VLANConfig) []sonic.Entry {
	vlanName := VLANName(vlanID)
	fields := map[string]string{
		"vlanid": fmt.Sprintf("%d", vlanID),
	}
	if opts.Description != "" {
		fields["description"] = opts.Description
	}

	entries := []sonic.Entry{
		{Table: "VLAN", Key: vlanName, Fields: fields},
	}

	if opts.L2VNI > 0 {
		entries = append(entries, createVniMapConfig(vlanName, opts.L2VNI)...)
	}

	return entries
}

// createVlanMemberConfig returns a CONFIG_DB VLAN_MEMBER entry for adding an
// interface to a VLAN with the specified tagging mode.
func createVlanMemberConfig(vlanID int, interfaceName string, tagged bool) []sonic.Entry {
	taggingMode := "untagged"
	if tagged {
		taggingMode = "tagged"
	}

	return []sonic.Entry{
		{Table: "VLAN_MEMBER", Key: VLANMemberKey(vlanID, interfaceName), Fields: map[string]string{
			"tagging_mode": taggingMode,
		}},
	}
}

// createSviConfig returns CONFIG_DB entries for an IRB: a VLAN_INTERFACE base entry
// with optional VRF binding, an optional IP address entry, and an optional
// SAG_GLOBAL entry for anycast gateway MAC.
func createSviConfig(vlanID int, opts IRBConfig) []sonic.Entry {
	vlanName := VLANName(vlanID)

	// VLAN_INTERFACE base entry with optional VRF binding
	fields := map[string]string{}
	if opts.VRF != "" {
		fields["vrf_name"] = opts.VRF
	}
	entries := []sonic.Entry{
		{Table: "VLAN_INTERFACE", Key: vlanName, Fields: fields},
	}

	// IP address binding
	if opts.IPAddress != "" {
		entries = append(entries, sonic.Entry{
			Table: "VLAN_INTERFACE", Key: IRBIPKey(vlanID, opts.IPAddress), Fields: map[string]string{},
		})
	}

	// Anycast gateway MAC (SAG)
	if opts.AnycastMAC != "" {
		entries = append(entries, sonic.Entry{
			Table: "SAG_GLOBAL", Key: "IPv4", Fields: map[string]string{
				"gwmac": opts.AnycastMAC,
			},
		})
	}

	return entries
}

// deleteSagGlobalConfig returns the delete entry for the SAG_GLOBAL IPv4 singleton.
func deleteSagGlobalConfig() []sonic.Entry {
	return []sonic.Entry{{Table: "SAG_GLOBAL", Key: "IPv4"}}
}

// deleteVlanMemberConfig returns the delete entry for a single VLAN member.
func deleteVlanMemberConfig(vlanID int, intfName string) []sonic.Entry {
	return []sonic.Entry{{Table: "VLAN_MEMBER", Key: VLANMemberKey(vlanID, intfName)}}
}

// deleteSviIPConfig returns the delete entry for a specific SVI IP binding.
func deleteSviIPConfig(vlanID int, ipAddr string) []sonic.Entry {
	return []sonic.Entry{{Table: "VLAN_INTERFACE", Key: IRBIPKey(vlanID, ipAddr)}}
}

// deleteSviBaseConfig returns the delete entry for a VLAN_INTERFACE base entry.
func deleteSviBaseConfig(vlanID int) []sonic.Entry {
	return []sonic.Entry{{Table: "VLAN_INTERFACE", Key: VLANName(vlanID)}}
}

// deleteVlanConfig returns the delete entry for a VLAN table entry.
// Unlike destroyVlan, this does not scan configDB for members or VNI mappings.
func deleteVlanConfig(vlanID int) []sonic.Entry {
	return []sonic.Entry{{Table: "VLAN", Key: VLANName(vlanID)}}
}

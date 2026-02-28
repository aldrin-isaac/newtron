package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func newActionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "actions [action]",
		Short: "List available test actions or show action details",
		Long: `List all available test step actions or show details for a specific action.

Without arguments, lists all actions with brief descriptions.
With an action name, shows detailed information about that action including
required and optional parameters.

Examples:
  newtrun actions                    # list all actions
  newtrun actions verify-ping        # show verify-ping action details
  newtrun actions bind-macvpn        # show bind-macvpn action details`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listActions()
			}
			return showActionDetails(args[0])
		},
	}
	return cmd
}

func listActions() error {
	actions := getActionMetadata()
	
	// Sort by name
	var names []string
	for name := range actions {
		names = append(names, name)
	}
	sort.Strings(names)

	// Group actions by category
	categories := map[string][]string{
		"Provisioning":   {},
		"Verification":   {},
		"VLAN":          {},
		"VRF":           {},
		"EVPN":          {},
		"Service":       {},
		"QoS":           {},
		"BGP":           {},
		"ACL":           {},
		"PortChannel":   {},
		"Interface":     {},
		"Routing":       {},
		"Host":          {},
		"Utility":       {},
	}

	for _, name := range names {
		meta := actions[name]
		categories[meta.Category] = append(categories[meta.Category], name)
	}

	fmt.Println("Available Test Actions:")
	fmt.Println()

	categoryOrder := []string{
		"Provisioning", "Verification", "VLAN", "VRF", "EVPN", "Service",
		"QoS", "BGP", "ACL", "PortChannel", "Interface", "Routing", "Host", "Utility",
	}

	for _, cat := range categoryOrder {
		acts := categories[cat]
		if len(acts) == 0 {
			continue
		}
		// Category header in bold cyan
		fmt.Printf("\033[1;36m%s:\033[0m\n", cat)
		for _, name := range acts {
			meta := actions[name]
			// Action name in green, description in default
			fmt.Printf("  \033[32m%-30s\033[0m %s\n", name, meta.ShortDesc)
		}
		fmt.Println()
	}

	fmt.Printf("\033[2mUse 'newtrun actions <action>' for detailed information about a specific action.\033[0m\n")
	return nil
}

func showActionDetails(actionName string) error {
	actions := getActionMetadata()
	meta, ok := actions[actionName]
	if !ok {
		return fmt.Errorf("unknown action: %s\n\nUse 'newtrun actions' to see available actions", actionName)
	}

	// Action name in bold green
	fmt.Printf("\033[1;32mAction:\033[0m %s\n", actionName)
	// Category in cyan
	fmt.Printf("\033[36mCategory:\033[0m %s\n", meta.Category)
	fmt.Printf("Description: %s\n", meta.LongDesc)

	if len(meta.Prerequisites) > 0 {
		// Prerequisites header in bold yellow
		fmt.Println("\n\033[1;33mPrerequisites:\033[0m")
		for _, p := range meta.Prerequisites {
			// Bullet in yellow
			fmt.Printf("  \033[33m•\033[0m %s\n", p)
		}
	}

	if len(meta.RequiredParams) > 0 {
		// Required params header in bold red
		fmt.Println("\n\033[1;31mRequired Parameters:\033[0m")
		for _, p := range meta.RequiredParams {
			// Param name in bold
			fmt.Printf("  \033[1m%-20s\033[0m %s\n", p.Name, p.Desc)
		}
	}

	if len(meta.OptionalParams) > 0 {
		// Optional params header in bold blue
		fmt.Println("\n\033[1;34mOptional Parameters:\033[0m")
		for _, p := range meta.OptionalParams {
			// Param name in bold
			fmt.Printf("  \033[1m%-20s\033[0m %s\n", p.Name, p.Desc)
		}
	}

	if len(meta.Devices) > 0 {
		fmt.Printf("\n\033[1mDevices:\033[0m %s\n", meta.Devices)
	}

	if meta.Example != "" {
		// Example header in bold magenta
		fmt.Printf("\n\033[1;35mExample:\033[0m\n%s\n", meta.Example)
	}

	return nil
}

type ActionMetadata struct {
	Category       string
	ShortDesc      string
	LongDesc       string
	Prerequisites  []string
	RequiredParams []ParamInfo
	OptionalParams []ParamInfo
	Devices        string
	Example        string
}

type ParamInfo struct {
	Name string
	Desc string
}

// getActionMetadata returns metadata for all test actions.
//
// MAINTENANCE NOTE: This metadata is manually maintained and must be kept in
// sync with stepValidations map in pkg/newtrun/parser.go. When adding a new
// action, update both:
//   1. Add action to stepValidations in parser.go
//   2. Add metadata entry here with category, description, parameters, example
//
// Future enhancement: Consider auto-generating this from stepValidations map
// using struct tags or reflection to eliminate duplication.
func getActionMetadata() map[string]ActionMetadata {
	return map[string]ActionMetadata{
		// Provisioning
		"provision": {
			Category:  "Provisioning",
			ShortDesc: "Provision device with baseline config",
			LongDesc:  "Applies baseline provisioning to a device including hostname, loopback, underlay BGP",
			Prerequisites: []string{
				"Device profile in profiles/<device>.json with: hostname, loopback_ip, underlay_asn, router_id, platform",
				"Topology links defined in topology.json (for BGP neighbor derivation)",
			},
			Devices: "required",
			Example: `- name: provision-leaf1
  action: provision
  devices: [leaf1]`,
		},
		"configure-loopback": {
			Category:  "Provisioning",
			ShortDesc: "Configure Loopback0 interface",
			LongDesc:  "Creates Loopback0 with the device's loopback IP from the resolved profile",
			Devices:   "required",
			Example: `- name: configure-loopback
  action: configure-loopback
  devices: all`,
		},
		"verify-provisioning": {
			Category:  "Verification",
			ShortDesc: "Verify device is fully provisioned",
			LongDesc:  "Checks that device provisioning is complete (hostname, loopback, BGP)",
			Devices:   "required",
			Example: `- name: verify-provision
  action: verify-provisioning
  devices: [leaf1]`,
		},
		"apply-frr-defaults": {
			Category:  "Provisioning",
			ShortDesc: "Apply FRR default configuration",
			LongDesc:  "Applies default FRR configuration to device",
			Devices:   "required",
			Example: `- name: apply-frr
  action: apply-frr-defaults
  devices: [leaf1, leaf2]`,
		},

		// Verification
		"verify-ping": {
			Category:  "Verification",
			ShortDesc: "Verify ICMP connectivity",
			LongDesc:  "Tests ICMP connectivity from device to target IP",
			Devices:   "required (single device)",
			RequiredParams: []ParamInfo{
				{"target", "Target IP address to ping"},
			},
			OptionalParams: []ParamInfo{
				{"count", "Number of ping packets (default: 5)"},
				{"success_rate", "Required success rate (default: 0.8)"},
			},
			Example: `- name: ping-test
  action: verify-ping
  devices: [host1]
  target: 192.168.1.1
  count: 10
  expect:
    success_rate: 0.90`,
		},
		"verify-bgp": {
			Category:  "Verification",
			ShortDesc: "Verify BGP session state",
			LongDesc:  "Checks BGP session state on device",
			Devices:   "required",
			OptionalParams: []ParamInfo{
				{"state", "Expected BGP state (e.g., Established)"},
				{"timeout", "Timeout for state check"},
			},
			Example: `- name: verify-bgp-up
  action: verify-bgp
  devices: [leaf1]
  expect:
    state: Established
    timeout: 30s`,
		},
		"verify-health": {
			Category:  "Verification",
			ShortDesc: "Verify device health",
			LongDesc:  "Checks device health status via STATE_DB",
			Devices:   "required",
			Example: `- name: health-check
  action: verify-health
  devices: [leaf1, leaf2]`,
		},
		"verify-route": {
			Category:  "Verification",
			ShortDesc: "Verify route in routing table",
			LongDesc:  "Checks that a specific route prefix exists in APP_DB routing table",
			Devices:   "required (single device)",
			RequiredParams: []ParamInfo{
				{"prefix", "Route prefix to verify"},
				{"vrf", "VRF name (or 'default')"},
			},
			OptionalParams: []ParamInfo{
				{"timeout", "Timeout for route appearance"},
				{"poll_interval", "Polling interval"},
			},
			Example: `- name: verify-route
  action: verify-route
  devices: [leaf1]
  prefix: 10.1.200.0/24
  vrf: default
  expect:
    timeout: 30s
    poll_interval: 3s`,
		},
		"verify-config-db": {
			Category:  "Verification",
			ShortDesc: "Verify CONFIG_DB entry",
			LongDesc:  "Checks that a specific entry exists in SONiC CONFIG_DB",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"table", "CONFIG_DB table name"},
			},
			OptionalParams: []ParamInfo{
				{"key", "Table key to check"},
				{"exists", "Whether entry should exist (true/false)"},
				{"min_entries", "Minimum number of entries"},
				{"fields", "Expected field values"},
			},
			Example: `- name: verify-vxlan-tunnel
  action: verify-config-db
  devices: [leaf1]
  table: VXLAN_TUNNEL
  expect:
    exists: true
    min_entries: 1`,
		},
		"verify-state-db": {
			Category:  "Verification",
			ShortDesc: "Verify STATE_DB entry fields",
			LongDesc:  "Checks specific field values in SONiC STATE_DB",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"table", "STATE_DB table name"},
				{"key", "Table key"},
			},
			OptionalParams: []ParamInfo{
				{"fields", "Expected field values"},
			},
			Example: `- name: verify-port-state
  action: verify-state-db
  devices: [leaf1]
  table: PORT_TABLE
  key: Ethernet0
  expect:
    fields:
      oper_status: up`,
		},

		// VLAN
		"create-vlan": {
			Category:  "VLAN",
			ShortDesc: "Create a VLAN",
			LongDesc:  "Creates a VLAN on the device",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vlan_id", "VLAN ID (1-4094)"},
			},
			Example: `- name: create-vlan-100
  action: create-vlan
  devices: [leaf1]
  vlan_id: 100`,
		},
		"delete-vlan": {
			Category:  "VLAN",
			ShortDesc: "Delete a VLAN",
			LongDesc:  "Deletes a VLAN from the device",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vlan_id", "VLAN ID to delete"},
			},
			Example: `- name: delete-vlan-100
  action: delete-vlan
  devices: [leaf1]
  vlan_id: 100`,
		},
		"add-vlan-member": {
			Category:  "VLAN",
			ShortDesc: "Add interface to VLAN",
			LongDesc:  "Adds an interface as a member of a VLAN",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vlan_id", "VLAN ID"},
				{"interface", "Interface name"},
			},
			OptionalParams: []ParamInfo{
				{"tagging", "Tagged or untagged (default: untagged)"},
			},
			Example: `- name: add-member
  action: add-vlan-member
  devices: [leaf1]
  vlan_id: 100
  interface: Ethernet1
  tagging: untagged`,
		},
		"remove-vlan-member": {
			Category:  "VLAN",
			ShortDesc: "Remove interface from VLAN",
			LongDesc:  "Removes an interface from VLAN membership",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vlan_id", "VLAN ID"},
				{"interface", "Interface name"},
			},
			Example: `- name: remove-member
  action: remove-vlan-member
  devices: [leaf1]
  vlan_id: 100
  interface: Ethernet1`,
		},
		"configure-svi": {
			Category:  "VLAN",
			ShortDesc: "Configure VLAN SVI (IRB)",
			LongDesc:  "Configures a VLAN interface with IP address and optional anycast gateway",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vlan_id", "VLAN ID"},
			},
			OptionalParams: []ParamInfo{
				{"ip", "IP address with prefix (e.g., 192.168.1.1/24)"},
				{"anycast", "Enable anycast gateway (true/false)"},
			},
			Example: `- name: configure-svi-100
  action: configure-svi
  devices: [leaf1]
  vlan_id: 100
  params:
    ip: 192.168.100.1/24
    anycast: true`,
		},

		// VRF
		"create-vrf": {
			Category:  "VRF",
			ShortDesc: "Create a VRF",
			LongDesc:  "Creates a VRF instance on the device",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vrf", "VRF name"},
			},
			Example: `- name: create-vrf-cust1
  action: create-vrf
  devices: [leaf1]
  vrf: Vrf_CUST1`,
		},
		"delete-vrf": {
			Category:  "VRF",
			ShortDesc: "Delete a VRF",
			LongDesc:  "Deletes a VRF instance from the device",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vrf", "VRF name"},
			},
			Example: `- name: delete-vrf-cust1
  action: delete-vrf
  devices: [leaf1]
  vrf: Vrf_CUST1`,
		},
		"add-vrf-interface": {
			Category:  "VRF",
			ShortDesc: "Add interface to VRF",
			LongDesc:  "Binds an interface to a VRF",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vrf", "VRF name"},
				{"interface", "Interface name"},
			},
			Example: `- name: add-to-vrf
  action: add-vrf-interface
  devices: [leaf1]
  vrf: Vrf_CUST1
  interface: Ethernet10`,
		},
		"remove-vrf-interface": {
			Category:  "VRF",
			ShortDesc: "Remove interface from VRF",
			LongDesc:  "Unbinds an interface from a VRF",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vrf", "VRF name"},
				{"interface", "Interface name"},
			},
			Example: `- name: remove-from-vrf
  action: remove-vrf-interface
  devices: [leaf1]
  vrf: Vrf_CUST1
  interface: Ethernet10`,
		},

		// EVPN
		"setup-evpn": {
			Category:  "EVPN",
			ShortDesc: "Setup EVPN control plane",
			LongDesc:  "Configures EVPN overlay including loopback peering and route reflector",
			Prerequisites: []string{
				"Device profile with: loopback_ip (VTEP source), bgp_neighbors (route reflectors), underlay_asn, router_id",
				"Underlay BGP sessions already established (use 'provision' first)",
			},
			Devices: "required",
			RequiredParams: []ParamInfo{
				{"source_ip", "VTEP source IP (loopback address)"},
			},
			Example: `- name: setup-evpn-leaf1
  action: setup-evpn
  devices: [leaf1]
  source_ip: 10.0.0.11`,
		},
		"bind-ipvpn": {
			Category:  "EVPN",
			ShortDesc: "Bind VRF to IP-VPN (EVPN L3VNI)",
			LongDesc:  "Binds a VRF to an IP-VPN spec, creating VXLAN tunnel mapping for L3 overlay",
			Prerequisites: []string{
				"IPVPNSpec defined in network.json, zone spec, or device profile (under 'ipvpns' section)",
				"VRF already created (use 'create-vrf' first)",
				"EVPN control plane configured (use 'setup-evpn' first)",
			},
			Devices: "required",
			RequiredParams: []ParamInfo{
				{"vrf", "VRF name"},
				{"ipvpn", "IP-VPN spec name"},
			},
			Example: `- name: bind-vrf-ipvpn
  action: bind-ipvpn
  devices: [leaf1]
  vrf: Vrf_CUST1
  ipvpn: customer-l3`,
		},
		"unbind-ipvpn": {
			Category:  "EVPN",
			ShortDesc: "Unbind VRF from IP-VPN",
			LongDesc:  "Removes IP-VPN binding from a VRF",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vrf", "VRF name"},
			},
			Example: `- name: unbind-vrf-ipvpn
  action: unbind-ipvpn
  devices: [leaf1]
  vrf: Vrf_CUST1`,
		},
		"bind-macvpn": {
			Category:  "EVPN",
			ShortDesc: "Bind VLAN to MAC-VPN (EVPN L2VNI)",
			LongDesc:  "Binds a VLAN to a MAC-VPN spec, creating VXLAN tunnel mapping for L2 overlay",
			Prerequisites: []string{
				"MACVPNSpec defined in network.json, zone spec, or device profile (under 'macvpns' section)",
				"VLAN already created (use 'create-vlan' first)",
				"EVPN control plane configured (use 'setup-evpn' first)",
			},
			Devices: "required",
			RequiredParams: []ParamInfo{
				{"vlan_id", "VLAN ID to bind"},
				{"macvpn", "MAC-VPN spec name"},
			},
			Example: `- name: bind-vlan100
  action: bind-macvpn
  devices: [leaf1]
  vlan_id: 100
  params:
    macvpn: servers-vlan100`,
		},
		"unbind-macvpn": {
			Category:  "EVPN",
			ShortDesc: "Unbind VLAN from MAC-VPN",
			LongDesc:  "Removes MAC-VPN binding from a VLAN",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vlan_id", "VLAN ID to unbind"},
			},
			Example: `- name: unbind-vlan100
  action: unbind-macvpn
  devices: [leaf1]
  vlan_id: 100`,
		},

		// Service
		"apply-service": {
			Category:  "Service",
			ShortDesc: "Apply service to interface",
			LongDesc:  "Applies a service spec to an interface (configures IP, VRF binding, BGP, ACL, QoS)",
			Prerequisites: []string{
				"ServiceSpec defined in network.json, zone spec, or device profile (under 'services' section)",
				"Referenced VRF exists (if service specifies VRF binding)",
				"Referenced Filter/QoS specs exist (if service references them)",
			},
			Devices: "required",
			RequiredParams: []ParamInfo{
				{"interface", "Interface name"},
				{"service", "Service spec name"},
			},
			OptionalParams: []ParamInfo{
				{"ip", "IP address override"},
				{"vlan", "VLAN ID override"},
			},
			Example: `- name: apply-customer-service
  action: apply-service
  devices: [leaf1]
  interface: Ethernet10
  service: customer-l3
  params:
    ip: 10.1.1.1/30`,
		},
		"remove-service": {
			Category:  "Service",
			ShortDesc: "Remove service from interface",
			LongDesc:  "Removes service configuration from an interface",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"interface", "Interface name"},
			},
			Example: `- name: remove-service
  action: remove-service
  devices: [leaf1]
  interface: Ethernet10`,
		},
		"refresh-service": {
			Category:  "Service",
			ShortDesc: "Refresh service configuration",
			LongDesc:  "Re-applies service configuration to interface (idempotent update)",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"interface", "Interface name"},
			},
			Example: `- name: refresh
  action: refresh-service
  devices: [leaf1]
  interface: Ethernet10`,
		},

		// QoS
		"apply-qos": {
			Category:  "QoS",
			ShortDesc: "Apply QoS policy to interface",
			LongDesc:  "Applies a QoS policy to an interface",
			Prerequisites: []string{
				"QoS policy spec defined in network.json, zone spec, or device profile (under 'qos_policies' section)",
			},
			Devices: "required",
			RequiredParams: []ParamInfo{
				{"interface", "Interface name"},
				{"qos_policy", "QoS policy name"},
			},
			Example: `- name: apply-qos
  action: apply-qos
  devices: [leaf1]
  interface: Ethernet10
  qos_policy: datacenter`,
		},
		"remove-qos": {
			Category:  "QoS",
			ShortDesc: "Remove QoS policy from interface",
			LongDesc:  "Removes QoS policy from an interface",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"interface", "Interface name"},
			},
			Example: `- name: remove-qos
  action: remove-qos
  devices: [leaf1]
  interface: Ethernet10`,
		},

		// BGP
		"bgp-add-neighbor": {
			Category:  "BGP",
			ShortDesc: "Add BGP neighbor to interface",
			LongDesc:  "Configures BGP neighbor on an interface",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"remote_asn", "Remote AS number"},
			},
			OptionalParams: []ParamInfo{
				{"interface", "Interface name (for interface BGP)"},
				{"vrf", "VRF name (default: global)"},
			},
			Example: `- name: add-bgp-neighbor
  action: bgp-add-neighbor
  devices: [leaf1]
  interface: Ethernet10
  remote_asn: 65100`,
		},
		"bgp-remove-neighbor": {
			Category:  "BGP",
			ShortDesc: "Remove BGP neighbor",
			LongDesc:  "Removes BGP neighbor configuration",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"neighbor_ip", "Neighbor IP address"},
			},
			OptionalParams: []ParamInfo{
				{"interface", "Interface name (for interface BGP)"},
			},
			Example: `- name: remove-bgp-neighbor
  action: bgp-remove-neighbor
  devices: [leaf1]
  neighbor_ip: 10.1.1.2`,
		},

		// PortChannel (LAG)
		"create-portchannel": {
			Category:  "PortChannel",
			ShortDesc: "Create port channel (LAG)",
			LongDesc:  "Creates a port channel (Link Aggregation Group)",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"name", "PortChannel name (e.g., PortChannel1)"},
			},
			OptionalParams: []ParamInfo{
				{"members", "List of member interfaces"},
				{"mtu", "MTU size"},
				{"min_links", "Minimum number of links"},
				{"fallback", "Enable fallback mode"},
				{"fast_rate", "Enable fast LACP rate"},
			},
			Example: `- name: create-lag
  action: create-portchannel
  devices: [leaf1]
  name: PortChannel1
  members: [Ethernet0, Ethernet1]
  min_links: 1`,
		},
		"delete-portchannel": {
			Category:  "PortChannel",
			ShortDesc: "Delete port channel",
			LongDesc:  "Deletes a port channel",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"name", "PortChannel name"},
			},
			Example: `- name: delete-lag
  action: delete-portchannel
  devices: [leaf1]
  name: PortChannel1`,
		},
		"add-portchannel-member": {
			Category:  "PortChannel",
			ShortDesc: "Add interface to port channel",
			LongDesc:  "Adds a member interface to a port channel",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"name", "PortChannel name"},
				{"member", "Interface to add"},
			},
			Example: `- name: add-lag-member
  action: add-portchannel-member
  devices: [leaf1]
  name: PortChannel1
  member: Ethernet2`,
		},
		"remove-portchannel-member": {
			Category:  "PortChannel",
			ShortDesc: "Remove interface from port channel",
			LongDesc:  "Removes a member interface from a port channel",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"name", "PortChannel name"},
				{"member", "Interface to remove"},
			},
			Example: `- name: remove-lag-member
  action: remove-portchannel-member
  devices: [leaf1]
  name: PortChannel1
  member: Ethernet2`,
		},

		// Interface
		"set-interface": {
			Category:  "Interface",
			ShortDesc: "Set interface property",
			LongDesc:  "Sets a property on an interface (mtu, admin_status, description, speed)",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"interface", "Interface name"},
				{"property", "Property name (mtu, admin_status, description, speed)"},
			},
			OptionalParams: []ParamInfo{
				{"value", "Property value"},
			},
			Example: `- name: set-mtu
  action: set-interface
  devices: [leaf1]
  interface: Ethernet0
  params:
    property: mtu
    value: 9000`,
		},

		// Routing
		"add-static-route": {
			Category:  "Routing",
			ShortDesc: "Add static route",
			LongDesc:  "Adds a static route to a VRF",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vrf", "VRF name"},
				{"prefix", "Destination prefix"},
				{"next_hop", "Next hop IP address"},
			},
			Example: `- name: add-route
  action: add-static-route
  devices: [leaf1]
  vrf: Vrf_CUST1
  prefix: 10.99.0.0/16
  next_hop: 10.1.1.2`,
		},
		"remove-static-route": {
			Category:  "Routing",
			ShortDesc: "Remove static route",
			LongDesc:  "Removes a static route from a VRF",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vrf", "VRF name"},
				{"prefix", "Destination prefix"},
			},
			Example: `- name: remove-route
  action: remove-static-route
  devices: [leaf1]
  vrf: Vrf_CUST1
  prefix: 10.99.0.0/16`,
		},

		// Host
		"host-exec": {
			Category:  "Host",
			ShortDesc: "Execute command on host",
			LongDesc:  "Executes a shell command on a virtual host device",
			Devices:   "required (single host device)",
			RequiredParams: []ParamInfo{
				{"command", "Shell command to execute"},
			},
			OptionalParams: []ParamInfo{
				{"contains", "Expected output substring (for validation)"},
				{"success_rate", "For ping commands, required success rate"},
			},
			Example: `- name: configure-host-ip
  action: host-exec
  devices: [host1]
  command: |
    ip addr add 192.168.1.10/24 dev eth0
  expect:
    contains: ""`,
		},

		// Utility
		"wait": {
			Category:  "Utility",
			ShortDesc: "Wait for specified duration",
			LongDesc:  "Pauses test execution for a specified duration",
			RequiredParams: []ParamInfo{
				{"duration", "Duration to wait (e.g., 5s, 1m)"},
			},
			Example: `- name: wait-convergence
  action: wait
  duration: 10s`,
		},
		"cleanup": {
			Category:  "Utility",
			ShortDesc: "Remove orphaned resources",
			LongDesc:  "Removes orphaned ACLs, VRFs, and VNI mappings from device",
			Devices:   "required",
			OptionalParams: []ParamInfo{
				{"type", "Cleanup type (acl, vrf, vni, or empty for all)"},
			},
			Example: `- name: cleanup-devices
  action: cleanup
  devices: [leaf1, leaf2]`,
		},
		"ssh-command": {
			Category:  "Utility",
			ShortDesc: "Execute raw SSH command",
			LongDesc:  "Executes a raw shell command on device via SSH (use sparingly)",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"command", "Shell command to execute"},
			},
			Example: `- name: check-frr
  action: ssh-command
  devices: [leaf1]
  command: vtysh -c 'show bgp summary'
  expect:
    contains: "Established"`,
		},
		"restart-service": {
			Category:  "Utility",
			ShortDesc: "Restart SONiC service",
			LongDesc:  "Restarts a SONiC systemd service (bgp, swss, etc.)",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"service", "Service name (e.g., bgp, swss, syncd)"},
			},
			Example: `- name: restart-bgp
  action: restart-service
  devices: [leaf1]
  service: bgp`,
		},

		// BGP (additional)
		"configure-bgp": {
			Category:  "BGP",
			ShortDesc: "Write BGP globals from device profile",
			LongDesc:  "Writes BGP_GLOBALS, BGP_GLOBALS_AF, ROUTE_REDISTRIBUTE, and DEVICE_METADATA from the device profile",
			Devices:   "required",
			Example: `- name: configure-bgp
  action: configure-bgp
  devices: [switch1]`,
		},
		"remove-bgp-globals": {
			Category:  "BGP",
			ShortDesc: "Remove BGP instance and global config",
			LongDesc:  "Deletes BGP_GLOBALS, BGP_GLOBALS_AF, ROUTE_REDISTRIBUTE, and clears bgp_asn from DEVICE_METADATA (reverse of configure-bgp)",
			Devices:   "required",
			Example: `- name: remove-bgp
  action: remove-bgp-globals
  devices: [switch1, switch2]`,
		},

		// Loopback removal
		"remove-loopback": {
			Category:  "Provisioning",
			ShortDesc: "Remove Loopback0 interface",
			LongDesc:  "Deletes all LOOPBACK_INTERFACE entries for Loopback0 (reverse of configure-loopback)",
			Devices:   "required",
			Example: `- name: remove-loopback
  action: remove-loopback
  devices: [switch1, switch2]`,
		},

		// ACL
		"create-acl-table": {
			Category:  "ACL",
			ShortDesc: "Create an ACL table",
			LongDesc:  "Creates an ACL_TABLE entry in CONFIG_DB with type, stage, and optional description",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"name", "ACL table name (e.g., MY_ACL)"},
			},
			OptionalParams: []ParamInfo{
				{"type", "ACL type: L3 (default) or L3V6"},
				{"stage", "Pipeline stage: ingress (default) or egress"},
				{"description", "Human-readable description"},
			},
			Example: `- name: create-acl
  action: create-acl-table
  devices: [leaf1]
  params:
    name: MY_ACL
    type: L3
    stage: ingress`,
		},
		"add-acl-rule": {
			Category:  "ACL",
			ShortDesc: "Add a rule to an ACL table",
			LongDesc:  "Adds an ACL_RULE entry with match criteria and FORWARD/DROP action",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"name", "ACL table name"},
				{"rule", "Rule name (e.g., RULE_10)"},
				{"action", "Terminal action: permit/FORWARD or deny/DROP"},
			},
			OptionalParams: []ParamInfo{
				{"priority", "Rule priority (higher = evaluated first, default: 0)"},
				{"src_ip", "Source IP prefix (e.g., 10.0.0.0/8)"},
				{"dst_ip", "Destination IP prefix"},
				{"protocol", "Protocol: tcp, udp, icmp, or IP number"},
				{"src_port", "Layer-4 source port"},
				{"dst_port", "Layer-4 destination port"},
			},
			Example: `- name: add-deny-rule
  action: add-acl-rule
  devices: [leaf1]
  params:
    name: MY_ACL
    rule: RULE_10
    action: DROP
    priority: 10
    src_ip: "10.0.0.0/8"`,
		},
		"delete-acl-rule": {
			Category:  "ACL",
			ShortDesc: "Delete a rule from an ACL table",
			LongDesc:  "Removes a single ACL_RULE entry; the table itself remains",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"name", "ACL table name"},
				{"rule", "Rule name to delete"},
			},
			Example: `- name: delete-rule
  action: delete-acl-rule
  devices: [leaf1]
  params:
    name: MY_ACL
    rule: RULE_10`,
		},
		"delete-acl-table": {
			Category:  "ACL",
			ShortDesc: "Delete an ACL table and all its rules",
			LongDesc:  "Removes the ACL_TABLE entry and all associated ACL_RULE entries",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"name", "ACL table name to delete"},
			},
			Example: `- name: delete-acl
  action: delete-acl-table
  devices: [leaf1]
  params:
    name: MY_ACL`,
		},
		"bind-acl": {
			Category:  "ACL",
			ShortDesc: "Bind ACL to an interface",
			LongDesc:  "Adds the interface to the ACL table's port binding list",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"interface", "Interface name"},
				{"name", "ACL table name"},
				{"direction", "ingress or egress"},
			},
			Example: `- name: bind-acl
  action: bind-acl
  devices: [leaf1]
  interface: Ethernet10
  params:
    name: MY_ACL
    direction: ingress`,
		},
		"unbind-acl": {
			Category:  "ACL",
			ShortDesc: "Remove interface from ACL binding",
			LongDesc:  "Removes the interface from the ACL table's port binding list",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"interface", "Interface name"},
				{"name", "ACL table name"},
			},
			Example: `- name: unbind-acl
  action: unbind-acl
  devices: [leaf1]
  interface: Ethernet10
  params:
    name: MY_ACL`,
		},

		// EVPN (additional)
		"teardown-evpn": {
			Category:  "EVPN",
			ShortDesc: "Remove EVPN overlay and VTEP",
			LongDesc:  "Deletes EVPN overlay neighbors, L2VPN EVPN AF, NVO, and VXLAN tunnel (reverse of setup-evpn)",
			Devices:   "required",
			Example: `- name: teardown-evpn
  action: teardown-evpn
  devices: [leaf1, leaf2]`,
		},

		// VLAN (additional)
		"remove-svi": {
			Category:  "VLAN",
			ShortDesc: "Remove SVI from a VLAN",
			LongDesc:  "Deletes all VLAN_INTERFACE entries for the VLAN (reverse of configure-svi)",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"vlan_id", "VLAN ID whose SVI to remove"},
			},
			Example: `- name: remove-svi
  action: remove-svi
  devices: [leaf1]
  vlan_id: 300`,
		},

		// Interface (additional)
		"remove-ip": {
			Category:  "Interface",
			ShortDesc: "Remove IP address from interface",
			LongDesc:  "Deletes an IP address entry from an interface; removes the base entry if last IP",
			Devices:   "required",
			RequiredParams: []ParamInfo{
				{"interface", "Interface name"},
				{"ip", "IP address with prefix length (e.g., 10.1.0.0/31)"},
			},
			Example: `- name: remove-ip
  action: remove-ip
  devices: [switch1]
  interface: Ethernet0
  params:
    ip: "10.1.0.0/31"`,
		},
	}
}

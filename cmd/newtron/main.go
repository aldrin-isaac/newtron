// Newtron - SONiC Network Configuration Tool
//
// A CLI tool for managing SONiC network devices with:
//   - EVPN-based VPN configuration
//   - Service-based interface management
//   - Dry-run by default (preview changes, require -x to execute)
//   - Audit logging of all changes
//   - Permission-based access control
//
// OO CLI Pattern:
//
//	Context flags select the object; commands are methods on that object:
//
//	newtron -n <network> -d <device> -i <interface> <verb> [args] [-x]
//	         └─────────────┬─────────────────────┘   └──┬──┘
//	               Object Selection              Method Call
//
// Context flags:
//
//	-n, --network   Network name (or set default via: newtron settings set network <name>)
//	-d, --device    Device name (selects Device object)
//	-i, --interface Interface name (selects Interface object)
//
// Command verbs (symmetric read/write):
//
//	show/get          - Read object details or properties
//	set               - Write object properties
//	list/list-*       - List child objects or collection members
//	create/delete     - Object lifecycle
//	add-*/remove-*    - Collection operations
//	apply-*/remove-*  - Bind/unbind operations (service, baseline)
//	bind-*/unbind-*   - Bind/unbind operations (ACL)
//	map-*/unmap-*     - Mapping operations (VNI)
//
// Examples:
//
//	newtron -d leaf1-ny show                              # Device details
//	newtron -d leaf1-ny -i Ethernet0 show                 # Interface details
//	newtron -d leaf1-ny -i Ethernet0 get mtu              # Get specific property
//	newtron -d leaf1-ny -i Ethernet0 set mtu 9000         # Set property
//	newtron -d leaf1-ny -i Ethernet0 apply-service customer-l3 --ip 10.1.1.1/30
//	newtron -d leaf1-ny -i Ethernet0 get-service          # Get bound service
//	newtron -d leaf1-ny -i PortChannel100 add-member Ethernet0
//	newtron -d leaf1-ny -i PortChannel100 list-members    # List LAG members
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/audit"
	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/settings"
	"github.com/newtron-network/newtron/pkg/spec"
	"github.com/newtron-network/newtron/pkg/util"
	"github.com/newtron-network/newtron/pkg/version"
)

var (
	// Global context flags (set the scope for operations)
	networkName   string // -n, --network
	deviceName    string // -d, --device
	interfaceName string // -i, --interface

	// Global option flags
	specDir     string
	executeMode bool
	saveMode    bool
	verbose     bool
	jsonOutput  bool

	// Global state
	userSettings *settings.Settings
	loader       *spec.Loader
	net          *network.Network
	permChecker  *auth.Checker
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "newtron",
	Short: "SONiC Network Configuration Tool",
	Long: `Newtron is an object-oriented CLI tool for managing SONiC network devices.

OO Pattern: Context flags select the object; commands are methods on that object.

  newtron -d <device> -i <interface> <verb> [args] [-x]

Context Flags (Object Selection):
  -n, --network    Network name (defaults from settings)
  -d, --device     Device name (selects Device object)
  -i, --interface  Interface/LAG/VLAN name (selects Interface object)

Option Flags:
  -x, --execute    Execute changes (default is dry-run/preview)
  -s, --save       Save config to disk after executing changes (requires -x)
  -S, --specs      Specification directory
  -v, --verbose    Verbose output
      --json       JSON output format

Command Verbs (Object Methods):
  show, get <prop>     Read object details or specific property
  set <prop> <value>   Set object property
  list <type>          List child objects (interfaces, vlans, etc.)
  create <type>        Create new object
  delete <type>        Delete object
  add-member           Add member to collection (LAG/VLAN)
  remove-member        Remove member from collection
  list-members         List collection members
  apply-service        Bind service to interface
  remove-service       Unbind service from interface
  get-service          Get bound service name
  bind-acl             Bind ACL to interface
  unbind-acl           Unbind ACL from interface
  list-acls            List bound ACLs
  map-l2vni            Map VLAN to L2VNI
  unmap-l2vni          Unmap L2VNI (via: evpn unmap-l2vni)
  get-l2vni            Get L2VNI mapping
  configure-svi        Configure SVI (Layer 3 VLAN interface)

Examples:
  # Device-level operations
  newtron -d leaf1-ny show                           # Show device status
  newtron -d leaf1-ny list interfaces                # List interfaces
  newtron -d leaf1-ny create vlan 100 --name Servers # Create VLAN

  # Interface-level operations
  newtron -d leaf1-ny -i Ethernet0 show              # Show interface
  newtron -d leaf1-ny -i Ethernet0 get mtu           # Get MTU value
  newtron -d leaf1-ny -i Ethernet0 set mtu 9000 -x   # Set MTU
  newtron -d leaf1-ny -i Ethernet0 apply-service customer-l3 --ip 10.1.1.1/30 -x
  newtron -d leaf1-ny -i Ethernet0 get-service       # Get bound service

  # LAG operations
  newtron -d leaf1-ny create lag PortChannel100 --members Ethernet0,Ethernet4
  newtron -d leaf1-ny -i PortChannel100 add-member Ethernet8 -x
  newtron -d leaf1-ny -i PortChannel100 list-members # List LAG members

  # VLAN operations
  newtron -d leaf1-ny -i Vlan100 add-member Ethernet4 --tagged -x
  newtron -d leaf1-ny -i Vlan100 list-members        # List VLAN members
  newtron -d leaf1-ny -i Vlan100 map-l2vni 10100 -x
  newtron -d leaf1-ny -i Vlan100 get-l2vni           # Get VNI mapping

  # Interactive mode
  newtron interactive`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip initialization for certain commands
		if isSettingsOrHelp(cmd) {
			return nil
		}

		// Validate flag combinations
		if saveMode && !executeMode {
			return fmt.Errorf("--save (-s) requires --execute (-x): use -xs to execute and save")
		}

		// Load user settings
		var err error
		userSettings, err = settings.Load()
		if err != nil {
			util.Warnf("Could not load settings: %v", err)
			userSettings = &settings.Settings{}
		}

		// Apply defaults from settings
		if networkName == "" {
			networkName = userSettings.DefaultNetwork
		}
		if specDir == "" {
			specDir = userSettings.GetSpecDir()
		}

		// Set log level
		if verbose {
			util.SetLogLevel("debug")
		}

		// Initialize spec loader
		loader = spec.NewLoader(specDir)
		if err := loader.Load(); err != nil {
			return fmt.Errorf("loading specs: %w", err)
		}

		// Create Network object (the top-level object in OO hierarchy)
		net, err = network.NewNetwork(specDir)
		if err != nil {
			return fmt.Errorf("initializing network: %w", err)
		}

		// Initialize permission checker
		permChecker = auth.NewChecker(loader.GetNetwork())

		// Initialize audit logger
		auditPath := "/var/log/newtron/audit.log"
		if specDir != "" {
			auditPath = specDir + "/audit.log"
		}
		auditLogger, err := audit.NewFileLogger(auditPath, audit.RotationConfig{
			MaxSize:    10 * 1024 * 1024, // 10MB
			MaxBackups: 10,
		})
		if err != nil {
			util.Warnf("Could not initialize audit logging: %v", err)
		} else {
			audit.SetDefaultLogger(auditLogger)
		}

		return nil
	},
}

func init() {
	// Context flags (object selectors)
	rootCmd.PersistentFlags().StringVarP(&networkName, "network", "n", "", "Network name (object selector)")
	rootCmd.PersistentFlags().StringVarP(&deviceName, "device", "d", "", "Device name (object selector)")
	rootCmd.PersistentFlags().StringVarP(&interfaceName, "interface", "i", "", "Interface/LAG/VLAN name (object selector)")

	// Option flags
	rootCmd.PersistentFlags().StringVarP(&specDir, "specs", "S", "", "Specification directory")
	rootCmd.PersistentFlags().BoolVarP(&executeMode, "execute", "x", false, "Execute changes (default is dry-run)")
	rootCmd.PersistentFlags().BoolVarP(&saveMode, "save", "s", false, "Save config to disk after executing changes")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	// ============================================================================
	// OO Command Verbs (methods on the selected object)
	// ============================================================================

	// Read operations (symmetric with write operations)
	rootCmd.AddCommand(showCmd)        // show - display object details
	rootCmd.AddCommand(getCmd)         // get <property> - get specific property
	rootCmd.AddCommand(listCmd)        // list <type> - list child objects
	rootCmd.AddCommand(listMembersCmd) // list-members - list collection members
	rootCmd.AddCommand(listAclsCmd)    // list-acls - list bound ACLs
	rootCmd.AddCommand(getServiceCmd)  // get-service - get bound service
	rootCmd.AddCommand(getL2VniCmd)    // get-l2vni - get L2VNI mapping
	rootCmd.AddCommand(listBgpCmd)     // list-bgp-neighbors - list BGP neighbors

	// Write operations
	rootCmd.AddCommand(setCmd)            // set <property> <value>
	rootCmd.AddCommand(createCmd)         // create <type> <name>
	rootCmd.AddCommand(deleteCmd)         // delete <type> <name>
	rootCmd.AddCommand(addMemberCmd)      // add-member <interface>
	rootCmd.AddCommand(removeMemberCmd)   // remove-member <interface>
	rootCmd.AddCommand(applyServiceCmd)   // apply-service <service>
	rootCmd.AddCommand(removeServiceCmd)  // remove-service
	rootCmd.AddCommand(refreshServiceCmd) // refresh-service (sync to current definition)
	rootCmd.AddCommand(bindAclCmd)        // bind-acl <acl>
	rootCmd.AddCommand(unbindAclCmd)      // unbind-acl <acl>
	rootCmd.AddCommand(bindMacvpnCmd)     // bind-macvpn <macvpn-name>
	rootCmd.AddCommand(unbindMacvpnCmd)   // unbind-macvpn
	rootCmd.AddCommand(getMacvpnCmd)      // get-macvpn
	rootCmd.AddCommand(addBgpCmd)         // add-bgp-neighbor <asn>
	rootCmd.AddCommand(removeBgpCmd)      // remove-bgp-neighbor

	// Device-level operations
	rootCmd.AddCommand(healthCheckVerbCmd) // health-check
	rootCmd.AddCommand(applyBaselineCmd)   // apply-baseline <configlet>
	rootCmd.AddCommand(cleanupCmd)         // cleanup (remove orphaned configs)
	rootCmd.AddCommand(configureSVICmd)    // configure-svi <vlan-id>

	// ============================================================================
	// Legacy command groups (for backwards compatibility and grouping)
	// ============================================================================
	rootCmd.AddCommand(settingsCmd)
	rootCmd.AddCommand(serviceCmd)   // service list, service show (network-level)
	rootCmd.AddCommand(interfaceCmd) // interface list (device-level alias)
	rootCmd.AddCommand(lagCmd)       // lag list, lag create (device-level aliases)
	rootCmd.AddCommand(vlanCmd)      // vlan list, vlan create (device-level aliases)
	rootCmd.AddCommand(aclCmd)       // acl list, acl create (device-level aliases)
	rootCmd.AddCommand(evpnCmd)      // evpn list (device-level alias)
	rootCmd.AddCommand(bgpCmd)       // bgp neighbors (device-level alias)
	rootCmd.AddCommand(healthCmd)    // health check (device-level alias)
	rootCmd.AddCommand(baselineCmd)  // baseline list, baseline show (network-level)
	rootCmd.AddCommand(auditCmd)
	rootCmd.AddCommand(stateCmd)
	rootCmd.AddCommand(interactiveCmd)
	rootCmd.AddCommand(shellCmd)
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("newtron %s (%s)\n", version.Version, version.GitCommit)
	},
}

// ============================================================================
// Context Helpers - Get device/interface from flags or prompt
// ============================================================================

// requireDevice ensures a device is specified via -d flag
func requireDevice(ctx context.Context) (*network.Device, error) {
	if deviceName == "" {
		return nil, fmt.Errorf("device required: use -d <device> flag")
	}
	return net.ConnectDevice(ctx, deviceName)
}

// requireInterface ensures both device and interface are specified
func requireInterface(ctx context.Context) (*network.Device, *network.Interface, error) {
	if deviceName == "" {
		return nil, nil, fmt.Errorf("device required: use -d <device> flag")
	}
	if interfaceName == "" {
		return nil, nil, fmt.Errorf("interface required: use -i <interface> flag")
	}

	dev, err := net.ConnectDevice(ctx, deviceName)
	if err != nil {
		return nil, nil, err
	}
	// Normalize interface name (e.g., Eth0 -> Ethernet0, Po100 -> PortChannel100)
	normalizedName := util.NormalizeInterfaceName(interfaceName)
	intf, err := dev.GetInterface(normalizedName)
	if err != nil {
		return nil, nil, err
	}
	return dev, intf, nil
}

// getDevice returns the device from -d flag (for commands that accept device as arg or flag)
func getDevice(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if deviceName != "" {
		return deviceName, nil
	}
	return "", fmt.Errorf("device required: use -d <device> flag or provide as argument")
}

// getInterface returns the interface from -i flag (for commands that accept interface as arg or flag)
// The returned name is normalized to SONiC format (e.g., Eth0 -> Ethernet0)
func getInterface(args []string, offset int) (string, error) {
	var name string
	if len(args) > offset {
		name = args[offset]
	} else if interfaceName != "" {
		name = interfaceName
	} else {
		return "", fmt.Errorf("interface required: use -i <interface> flag or provide as argument")
	}
	// Normalize interface name (e.g., Eth0 -> Ethernet0, Po100 -> PortChannel100)
	return util.NormalizeInterfaceName(name), nil
}

// ============================================================================
// Output Helpers
// ============================================================================

// Helper to print dry-run notice
func printDryRunNotice() {
	if !executeMode {
		fmt.Println("\n" + yellow("DRY-RUN: No changes applied. Use -x to execute."))
	}
}

// executeAndSave applies a changeset and optionally saves the config.
// This is the standard post-apply flow for all CLI write commands.
func executeAndSave(ctx context.Context, cs *network.ChangeSet, dev *network.Device) error {
	if err := cs.Apply(dev); err != nil {
		return fmt.Errorf("execution failed: %w", err)
	}
	fmt.Println("\n" + green("Changes applied successfully."))

	if saveMode {
		fmt.Print("Saving configuration... ")
		if err := dev.SaveConfig(ctx); err != nil {
			fmt.Println(red("FAILED"))
			return fmt.Errorf("config save failed: %w", err)
		}
		fmt.Println(green("saved."))
	}
	return nil
}

// Helper to check execute permission
func checkExecutePermission(perm auth.Permission, ctx *auth.Context) error {
	if executeMode {
		return permChecker.Check(perm, ctx)
	}
	// Preview/dry-run only needs view permission
	return nil
}

// isSettingsOrHelp checks whether cmd (or any ancestor) is a settings, help, or version command.
func isSettingsOrHelp(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "help", "version", "settings":
			return true
		}
	}
	return false
}

// Color helpers — delegate to pkg/cli
func green(s string) string  { return cli.Green(s) }
func yellow(s string) string { return cli.Yellow(s) }
func red(s string) string    { return cli.Red(s) }
func bold(s string) string   { return cli.Bold(s) }

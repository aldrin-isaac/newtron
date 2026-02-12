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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:               "newtron",
	Short:             "SONiC Network Configuration Tool",
	SilenceUsage:      true,
	SilenceErrors:     true,
	CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
	Long: `Newtron is an object-oriented CLI for managing SONiC network devices.

Context flags select the object; commands are methods on that object.
Write commands preview changes by default — use -x to execute.

  newtron -d <device> -i <interface> <verb> [args] [-x]`,
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

		// Set log level: quiet by default, verbose on -v
		if verbose {
			util.SetLogLevel("debug")
		} else {
			util.SetLogLevel("warn")
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

	// Option flags (global)
	rootCmd.PersistentFlags().StringVarP(&specDir, "specs", "S", "", "Specification directory")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")

	// Write flags (-x/-s) and output flags (--json) are local to commands that use them.
	// Use addWriteFlags(cmd) and addOutputFlags(cmd) to register them.

	// Add write flags to verb commands that mutate state
	for _, cmd := range []*cobra.Command{
		setCmd, createCmd, deleteCmd, addMemberCmd, removeMemberCmd,
		applyServiceCmd, removeServiceCmd, refreshServiceCmd,
		bindAclCmd, unbindAclCmd, bindMacvpnCmd, unbindMacvpnCmd,
		addBgpCmd, removeBgpCmd, applyBaselineCmd, cleanupCmd, configureSVICmd,
		provisionCmd,
	} {
		addWriteFlags(cmd)
	}

	// Add output flags to verb commands that produce structured output
	for _, cmd := range []*cobra.Command{
		showCmd, getCmd, listCmd, listMembersCmd, listAclsCmd,
		getServiceCmd, getL2VniCmd, getMacvpnCmd, listBgpCmd,
		healthCheckVerbCmd,
	} {
		addOutputFlags(cmd)
	}

	// Noun-group aliases: inherit write/output flags for their subcommands
	for _, cmd := range []*cobra.Command{aclCmd, bgpCmd, lagCmd, vlanCmd, evpnCmd, baselineCmd} {
		addWriteFlags(cmd)
		addOutputFlags(cmd)
	}
	for _, cmd := range []*cobra.Command{serviceCmd, interfaceCmd, healthCmd} {
		addOutputFlags(cmd)
	}

	// ============================================================================
	// Command Groups
	// ============================================================================

	rootCmd.AddGroup(
		&cobra.Group{ID: "query", Title: "Object Operations:"},
		&cobra.Group{ID: "mutate", Title: "Resource Management:"},
		&cobra.Group{ID: "device", Title: "Device Operations:"},
		&cobra.Group{ID: "meta", Title: "Configuration & Meta:"},
	)

	// Object Operations (read/query)
	for _, cmd := range []*cobra.Command{
		showCmd, getCmd, listCmd, listMembersCmd, listAclsCmd,
		getServiceCmd, getL2VniCmd, getMacvpnCmd, listBgpCmd,
	} {
		cmd.GroupID = "query"
		rootCmd.AddCommand(cmd)
	}

	// Resource Management (write/mutate)
	for _, cmd := range []*cobra.Command{
		setCmd, createCmd, deleteCmd, addMemberCmd, removeMemberCmd,
		applyServiceCmd, removeServiceCmd, refreshServiceCmd,
		bindAclCmd, unbindAclCmd, bindMacvpnCmd, unbindMacvpnCmd,
		addBgpCmd, removeBgpCmd, configureSVICmd,
	} {
		cmd.GroupID = "mutate"
		rootCmd.AddCommand(cmd)
	}

	// Device Operations
	for _, cmd := range []*cobra.Command{
		provisionCmd, healthCheckVerbCmd, applyBaselineCmd, cleanupCmd, stateCmd,
	} {
		cmd.GroupID = "device"
		rootCmd.AddCommand(cmd)
	}

	// Configuration & Meta
	for _, cmd := range []*cobra.Command{settingsCmd, auditCmd, versionCmd} {
		cmd.GroupID = "meta"
		rootCmd.AddCommand(cmd)
	}

	// Noun-group aliases: hidden but still functional
	for _, cmd := range []*cobra.Command{
		serviceCmd, interfaceCmd, lagCmd, vlanCmd, aclCmd,
		evpnCmd, bgpCmd, healthCmd, baselineCmd,
	} {
		cmd.Hidden = true
		rootCmd.AddCommand(cmd)
	}

	// Premature commands (hidden)
	rootCmd.AddCommand(interactiveCmd)
	rootCmd.AddCommand(shellCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		printVersion("newtron")
	},
}

func printVersion(tool string) {
	if version.Version == "dev" {
		fmt.Printf("%s dev build (use 'make build' for version info)\n", tool)
	} else {
		fmt.Printf("%s %s (%s)\n", tool, version.Version, version.GitCommit)
	}
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

// withDeviceWrite handles boilerplate for device-level write commands.
// The callback receives a connected, locked device and returns a changeset.
// If changeset is nil, the helper returns nil (command handled its own output).
// If changeset is non-nil, the helper prints it and handles execute/dry-run.
func withDeviceWrite(fn func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error)) error {
	ctx := context.Background()
	dev, err := requireDevice(ctx)
	if err != nil {
		return err
	}
	defer dev.Disconnect()

	if err := dev.Lock(); err != nil {
		return fmt.Errorf("locking device: %w", err)
	}
	defer dev.Unlock()

	changeSet, err := fn(ctx, dev)
	if err != nil {
		return err
	}
	if changeSet == nil {
		return nil
	}

	fmt.Println("Changes to be applied:")
	fmt.Print(changeSet.String())

	if executeMode {
		return executeAndSave(ctx, changeSet, dev)
	}
	printDryRunNotice()
	return nil
}

// withInterfaceWrite handles boilerplate for interface-level write commands.
// Same contract as withDeviceWrite but also resolves the interface.
func withInterfaceWrite(fn func(ctx context.Context, dev *network.Device, intf *network.Interface) (*network.ChangeSet, error)) error {
	ctx := context.Background()
	dev, intf, err := requireInterface(ctx)
	if err != nil {
		return err
	}
	defer dev.Disconnect()

	if err := dev.Lock(); err != nil {
		return fmt.Errorf("locking device: %w", err)
	}
	defer dev.Unlock()

	changeSet, err := fn(ctx, dev, intf)
	if err != nil {
		return err
	}
	if changeSet == nil {
		return nil
	}

	fmt.Println("Changes to be applied:")
	fmt.Print(changeSet.String())

	if executeMode {
		return executeAndSave(ctx, changeSet, dev)
	}
	printDryRunNotice()
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

// addWriteFlags registers -x/--execute and -s/--save as local flags.
// For noun-group parent commands, these are PersistentFlags so subcommands inherit.
func addWriteFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	if cmd.HasSubCommands() {
		flags = cmd.PersistentFlags()
	}
	flags.BoolVarP(&executeMode, "execute", "x", false, "Execute changes (default is dry-run)")
	flags.BoolVarP(&saveMode, "save", "s", false, "Save config after changes (requires -x)")
}

// addOutputFlags registers --json as a local flag.
// For noun-group parent commands, this is a PersistentFlag so subcommands inherit.
func addOutputFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	if cmd.HasSubCommands() {
		flags = cmd.PersistentFlags()
	}
	flags.BoolVar(&jsonOutput, "json", false, "JSON output")
}

// Color helpers — delegate to pkg/cli
func green(s string) string  { return cli.Green(s) }
func yellow(s string) string { return cli.Yellow(s) }
func red(s string) string    { return cli.Red(s) }
func bold(s string) string   { return cli.Bold(s) }

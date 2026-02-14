// Newtron - SONiC Network Configuration Tool
//
// A CLI tool for managing SONiC network devices with:
//   - EVPN-based VPN configuration
//   - Service-based interface management
//   - Dry-run by default (preview changes, require -x to execute)
//   - Audit logging of all changes
//   - Permission-based access control
//
// Noun-group CLI Pattern:
//
//	newtron <device> <resource> <action> [args] [-x]
//
// The first argument is the device name unless it matches a known command.
// Commands that don't need a device (settings, version, service list) work without one.
//
// Examples:
//
//	newtron leaf1-ny show                              # Device details
//	newtron leaf1-ny interface list                    # List interfaces
//	newtron leaf1-ny interface set Ethernet0 mtu 9000 -x
//	newtron leaf1-ny vlan create 100 --name Servers -x
//	newtron leaf1-ny service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x
//	newtron leaf1-ny vrf add-neighbor Vrf_CUST1 Ethernet0 65100 -x
//	newtron leaf1-ny evpn setup -x
//	newtron service list                               # No device needed
//	newtron settings show                              # No device needed
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/audit"
	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/settings"
	"github.com/newtron-network/newtron/pkg/util"
	"github.com/newtron-network/newtron/pkg/version"
)

// App holds CLI state shared across all commands.
type App struct {
	// Context flags
	deviceName string

	// Option flags
	specDir     string
	executeMode bool
	saveMode    bool
	verbose     bool
	jsonOutput  bool

	// Initialized state (set in PersistentPreRunE)
	settings    *settings.Settings
	net         *network.Network
	permChecker *auth.Checker
}

var app = &App{}

func main() {
	// Implicit device name: if the first arg is not a known command or flag,
	// treat it as a device name. This lets users write:
	//   newtron leaf1 vlan list
	// instead of:
	//   newtron -d leaf1 vlan list
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") && !isKnownCommand(os.Args[1]) {
		os.Args = append([]string{os.Args[0], "-d", os.Args[1]}, os.Args[2:]...)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// isKnownCommand checks if a string matches a registered top-level command name.
func isKnownCommand(name string) bool {
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == name {
			return true
		}
		for _, alias := range cmd.Aliases {
			if alias == name {
				return true
			}
		}
	}
	return name == "help" || name == "completion"
}

var rootCmd = &cobra.Command{
	Use:               "newtron",
	Short:             "SONiC Network Configuration Tool",
	SilenceUsage:      true,
	SilenceErrors:     true,
	CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
	Long: `Newtron is a noun-group CLI for managing SONiC network devices.

Commands are organized by resource (vlan, lag, bgp, evpn, service, acl, etc.).
Write commands preview changes by default — use -x to execute.

  newtron <device> <resource> <action> [args] [-x]

The first argument is the device name unless it matches a known command.
Each resource takes its natural key as a positional argument:

  newtron leaf1 interface show Ethernet0
  newtron leaf1 vlan create 100
  newtron leaf1 vrf add-neighbor Vrf_CUST1 Ethernet0 65100 -x
  newtron leaf1 service apply Ethernet0 customer-l3
  newtron service list                           # no device needed
  newtron settings show                          # no device needed`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip initialization for certain commands
		if isSettingsOrHelp(cmd) {
			return nil
		}

		// Validate flag combinations
		if app.saveMode && !app.executeMode {
			return fmt.Errorf("--save (-s) requires --execute (-x): use -xs to execute and save")
		}

		// Load user settings
		var err error
		app.settings, err = settings.Load()
		if err != nil {
			util.Logger.Warnf("Could not load settings: %v", err)
			app.settings = &settings.Settings{}
		}

		// Apply defaults from settings
		if app.specDir == "" {
			app.specDir = app.settings.GetSpecDir()
		}

		// Set log level: quiet by default, verbose on -v
		if app.verbose {
			util.SetLogLevel("debug")
		} else {
			util.SetLogLevel("warn")
		}

		// Create Network object (the top-level object in OO hierarchy)
		app.net, err = network.NewNetwork(app.specDir)
		if err != nil {
			return fmt.Errorf("initializing network: %w", err)
		}

		// Initialize permission checker from network spec
		app.permChecker = auth.NewChecker(app.net.Spec())

		// Initialize audit logger (path and rotation from settings)
		auditPath := app.settings.GetAuditLogPath(app.specDir)
		auditLogger, err := audit.NewFileLogger(auditPath, audit.RotationConfig{
			MaxSize:    int64(app.settings.GetAuditMaxSizeMB()) * 1024 * 1024,
			MaxBackups: app.settings.GetAuditMaxBackups(),
		})
		if err != nil {
			util.Logger.Warnf("Could not initialize audit logging: %v", err)
		} else {
			audit.SetDefaultLogger(auditLogger)
		}

		return nil
	},
}

func init() {
	// Context flags (object selectors)
	rootCmd.PersistentFlags().StringVarP(&app.deviceName, "device", "d", "", "Device name")

	// Option flags (global)
	rootCmd.PersistentFlags().StringVarP(&app.specDir, "specs", "S", "", "Specification directory")
	rootCmd.PersistentFlags().BoolVarP(&app.verbose, "verbose", "v", false, "Verbose output")

	// Write flags (-x/-s) and output flags (--json) on noun-group parents
	// (PersistentFlags so subcommands inherit)
	for _, cmd := range []*cobra.Command{
		interfaceCmd, vlanCmd, lagCmd, aclCmd, evpnCmd, bgpCmd,
		vrfCmd, serviceCmd, baselineCmd, deviceCmd, qosCmd, filterCmd,
	} {
		addWriteFlags(cmd)
		addOutputFlags(cmd)
	}
	for _, cmd := range []*cobra.Command{healthCmd} {
		addOutputFlags(cmd)
	}

	// Top-level commands that need their own flags
	addOutputFlags(showCmd)
	addWriteFlags(provisionCmd)

	// ============================================================================
	// Command Groups
	// ============================================================================

	rootCmd.AddGroup(
		&cobra.Group{ID: "resource", Title: "Resource Commands:"},
		&cobra.Group{ID: "device", Title: "Device Operations:"},
		&cobra.Group{ID: "meta", Title: "Configuration & Meta:"},
	)

	// Resource Commands (noun-groups)
	for _, cmd := range []*cobra.Command{
		interfaceCmd, vlanCmd, lagCmd, aclCmd, evpnCmd, bgpCmd,
		vrfCmd, serviceCmd, baselineCmd, qosCmd, filterCmd,
	} {
		cmd.GroupID = "resource"
		rootCmd.AddCommand(cmd)
	}

	// Device Operations
	for _, cmd := range []*cobra.Command{
		showCmd, provisionCmd, healthCmd, deviceCmd,
	} {
		cmd.GroupID = "device"
		rootCmd.AddCommand(cmd)
	}

	// Configuration & Meta
	for _, cmd := range []*cobra.Command{settingsCmd, auditCmd, versionCmd} {
		cmd.GroupID = "meta"
		rootCmd.AddCommand(cmd)
	}

	// Premature commands (hidden)
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
	if app.deviceName == "" {
		return nil, fmt.Errorf("device required: use -d <device> flag")
	}
	return app.net.ConnectDevice(ctx, app.deviceName)
}

// ============================================================================
// Output Helpers
// ============================================================================

// Helper to print dry-run notice
func printDryRunNotice() {
	if !app.executeMode {
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

	if app.saveMode {
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
	if app.executeMode {
		return app.permChecker.Check(perm, ctx)
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

	fmt.Print(changeSet.Preview())

	if app.executeMode {
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
	flags.BoolVarP(&app.executeMode, "execute", "x", false, "Execute changes (default is dry-run)")
	flags.BoolVarP(&app.saveMode, "save", "s", false, "Save config after changes (requires -x)")
}

// addOutputFlags registers --json as a local flag.
// For noun-group parent commands, this is a PersistentFlag so subcommands inherit.
func addOutputFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	if cmd.HasSubCommands() {
		flags = cmd.PersistentFlags()
	}
	flags.BoolVar(&app.jsonOutput, "json", false, "JSON output")
}

// Color helpers — delegate to pkg/cli
func green(s string) string  { return cli.Green(s) }
func yellow(s string) string { return cli.Yellow(s) }
func red(s string) string    { return cli.Red(s) }
func bold(s string) string   { return cli.Bold(s) }

// defaultStr returns s if non-empty, otherwise def.
func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// dash returns s if non-empty, otherwise "-".
func dash(s string) string { return defaultStr(s, "-") }

// dashInt formats v as a decimal string if > 0, otherwise "-".
func dashInt(v int) string {
	if v <= 0 {
		return "-"
	}
	return strconv.Itoa(v)
}

// formatOperStatus colorizes operational status values.
func formatOperStatus(status string) string {
	switch strings.ToLower(status) {
	case "up", "oper_up", "active":
		return green(status)
	case "down":
		return red(status)
	case "":
		return yellow("n/a")
	default:
		return yellow(status)
	}
}

// formatAdminStatus colorizes administrative status values.
func formatAdminStatus(status string) string {
	switch strings.ToLower(status) {
	case "up":
		return green(status)
	case "down":
		return red(status)
	case "":
		return ""
	default:
		return status
	}
}

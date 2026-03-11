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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
	"github.com/newtron-network/newtron/pkg/newtron/client"
	"github.com/newtron-network/newtron/pkg/util"
	"github.com/newtron-network/newtron/pkg/version"
)

// App holds CLI state shared across all commands.
type App struct {
	// Context flags
	deviceName string

	// Option flags
	rootDir     string // -S flag: network root dir (contains specs/)
	specDir     string // resolved: rootDir/specs or rootDir (flat layout)
	serverURL   string // --server flag
	networkID   string // --network-id flag
	executeMode bool
	noSave      bool
	verbose     bool
	jsonOutput  bool

	// Initialized state (set in PersistentPreRunE)
	settings *newtron.UserSettings
	client *client.Client // HTTP client for all commands
}

var app = &App{}

func main() {
	// Implicit device name: if the first arg is not a known command or flag,
	// treat it as a device name. This lets users write:
	//   newtron leaf1 vlan list
	// instead of:
	//   newtron -D leaf1 vlan list
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") && !isKnownCommand(os.Args[1]) {
		os.Args = append([]string{os.Args[0], "-D", os.Args[1]}, os.Args[2:]...)
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

// resolveNetworkSpecDir determines the actual specs directory from a root dir.
// If rootDir/specs/network.json exists, specs live in rootDir/specs/ (nested layout).
// Otherwise, specs live directly in rootDir (flat layout / backwards compat).
func resolveNetworkSpecDir(rootDir string) string {
	if _, err := os.Stat(filepath.Join(rootDir, "specs", "network.json")); err == nil {
		return filepath.Join(rootDir, "specs")
	}
	return rootDir
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

The first argument is treated as a device name (equivalent to -D <device>)
unless it matches a known command. This lets you write:

  newtron leaf1 vlan list          instead of    newtron -D leaf1 vlan list

Commands that don't need a device work without one:

  newtron service list             # list service specs
  newtron settings show            # show settings

Examples:

  newtron leaf1 interface show Ethernet0
  newtron leaf1 vlan create 100 -x
  newtron leaf1 vrf add-neighbor Vrf_CUST1 Ethernet0 65100 -x
  newtron leaf1 service apply Ethernet0 customer-l3 -x`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip initialization for certain commands
		if isSettingsOrHelp(cmd) {
			return nil
		}

		// Validate flag combinations
		if app.noSave && !app.executeMode {
			return fmt.Errorf("--no-save requires --execute (-x)")
		}

		// Load user settings
		var err error
		app.settings, err = newtron.LoadSettings()
		if err != nil {
			util.Logger.Warnf("Could not load settings: %v", err)
			app.settings = &newtron.UserSettings{}
		}

		// Apply defaults from settings
		if app.rootDir == "" {
			app.rootDir = app.settings.GetSpecDir()
		}

		// Resolve spec dir from root dir (auto-detect nested vs flat layout)
		app.specDir = resolveNetworkSpecDir(app.rootDir)

		// Set log level: quiet by default, verbose on -v
		if app.verbose {
			util.SetLogLevel("debug")
		} else {
			util.SetLogLevel("warn")
		}

		// Resolve server URL: flag > env > settings > default
		if app.serverURL == "" {
			app.serverURL = os.Getenv("NEWTRON_SERVER")
		}
		if app.serverURL == "" {
			app.serverURL = app.settings.GetServerURL()
		}

		// Resolve network ID: flag > env > settings > default
		if app.networkID == "" {
			app.networkID = os.Getenv("NEWTRON_NETWORK_ID")
		}
		if app.networkID == "" {
			app.networkID = app.settings.GetNetworkID()
		}

		// Create HTTP client and register network
		app.client = client.New(app.serverURL, app.networkID)
		if err := app.client.RegisterNetwork(app.specDir); err != nil {
			return fmt.Errorf("registering network with server: %w", err)
		}

		// Initialize audit logger (path and rotation from settings)
		auditPath := app.settings.GetAuditLogPath(app.specDir)
		if err := newtron.InitAuditLogger(auditPath, app.settings.GetAuditMaxSizeMB(), app.settings.GetAuditMaxBackups()); err != nil {
			util.Logger.Warnf("Could not initialize audit logging: %v", err)
		}

		return nil
	},
}

func init() {
	// Context flags (object selectors)
	rootCmd.PersistentFlags().StringVarP(&app.deviceName, "device", "D", "", "Device name")

	// Option flags (global)
	rootCmd.PersistentFlags().StringVarP(&app.rootDir, "specs", "S", "", "Network root directory (contains specs/)")
	rootCmd.PersistentFlags().StringVar(&app.serverURL, "server", "", "newtron-server URL (env: NEWTRON_SERVER)")
	rootCmd.PersistentFlags().StringVarP(&app.networkID, "network-id", "N", "", "Network identifier (env: NEWTRON_NETWORK_ID)")
	rootCmd.PersistentFlags().BoolVarP(&app.verbose, "verbose", "v", false, "Verbose output")

	// Write flags (-x/-s) and output flags (--json) on noun-group parents
	// (PersistentFlags so subcommands inherit)
	for _, cmd := range []*cobra.Command{
		interfaceCmd, vlanCmd, lagCmd, aclCmd, evpnCmd, bgpCmd,
		vrfCmd, serviceCmd, deviceCmd, qosCmd, filterCmd,
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
		vrfCmd, serviceCmd, qosCmd, filterCmd,
	} {
		cmd.GroupID = "resource"
		rootCmd.AddCommand(cmd)
	}

	// Device Operations
	for _, cmd := range []*cobra.Command{
		showCmd, provisionCmd, healthCmd, deviceCmd, initCmd,
	} {
		cmd.GroupID = "device"
		rootCmd.AddCommand(cmd)
	}

	// Configuration & Meta
	for _, cmd := range []*cobra.Command{settingsCmd, auditCmd, platformCmd, profileCmd, zoneCmd, versionCmd} {
		cmd.GroupID = "meta"
		rootCmd.AddCommand(cmd)
	}
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

// requireDevice ensures a device is specified via -D flag.
func requireDevice() error {
	if app.deviceName == "" {
		return fmt.Errorf("device required: use -D <device> flag")
	}
	return nil
}

// execOpts returns ExecOpts from the current app flags.
func execOpts() newtron.ExecOpts {
	return newtron.ExecOpts{Execute: app.executeMode, NoSave: app.noSave}
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

// displayWriteResult shows the result of a write operation.
// It handles preview, applied status, verification, and dry-run notices.
func displayWriteResult(result *newtron.WriteResult, err error) error {
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	if result.Preview != "" {
		fmt.Print(result.Preview)
	}
	if result.Applied {
		fmt.Println(green("Changes applied successfully."))
	}
	if result.Verification != nil {
		printVerification(result.Verification)
	}
	if !app.executeMode {
		printDryRunNotice()
	}
	return nil
}

// printVerification displays verification results to the user.
func printVerification(v *newtron.VerificationResult) {
	total := v.Passed + v.Failed
	fmt.Print("Verifying... ")
	if v.Failed > 0 {
		fmt.Printf("%s (%d/%d entries verified, %d failed)\n", red("FAILED"), v.Passed, total, v.Failed)
		for _, e := range v.Errors {
			if e.Actual == "" {
				fmt.Printf("  [MISSING] %s|%s  (field: %s, expected: %q)\n",
					e.Table, e.Key, e.Field, e.Expected)
			} else {
				fmt.Printf("  [MISMATCH] %s|%s  (field: %s, expected: %q, actual: %q)\n",
					e.Table, e.Key, e.Field, e.Expected, e.Actual)
			}
		}
	} else {
		fmt.Printf("%s (%d/%d entries verified)\n", green("OK"), v.Passed, total)
	}
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

// addWriteFlags registers -x/--execute and --no-save as local flags.
// For noun-group parent commands, these are PersistentFlags so subcommands inherit.
func addWriteFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	if cmd.HasSubCommands() {
		flags = cmd.PersistentFlags()
	}
	flags.BoolVarP(&app.executeMode, "execute", "x", false, "Execute changes (default is dry-run)")
	flags.BoolVar(&app.noSave, "no-save", false, "Skip config save after execution (requires -x)")
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

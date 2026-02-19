package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

var platformCmd = &cobra.Command{
	Use:   "platform",
	Short: "Platform information and feature support",
	Long: `Query platform definitions and feature support.

Shows which platforms are available and which features are supported
by each platform. Useful for understanding platform capabilities and
troubleshooting test scenario skips.`,
}

var platformListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all defined platforms",
	Long: `List all platforms defined in platforms.json.

Shows platform name, HWSKU, description, and count of unsupported features.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if app.net == nil {
			return fmt.Errorf("network not initialized")
		}

		platforms := app.net.Platforms()
		if len(platforms) == 0 {
			fmt.Println("No platforms defined")
			return nil
		}

		// Sort by name
		var names []string
		for name := range platforms {
			names = append(names, name)
		}
		sort.Strings(names)

		if app.jsonOutput {
			// TODO: JSON output
			return fmt.Errorf("JSON output not yet implemented")
		}

		// Table output
		fmt.Printf("%-20s %-30s %-40s %s\n", "PLATFORM", "HWSKU", "DESCRIPTION", "UNSUPPORTED")
		fmt.Printf("%-20s %-30s %-40s %s\n", "--------", "-----", "-----------", "-----------")

		for _, name := range names {
			p := platforms[name]
			hwsku := p.HWSKU
			if hwsku == "" {
				hwsku = "-"
			}
			desc := p.Description
			if desc == "" {
				desc = "-"
			}
			if len(desc) > 40 {
				desc = desc[:37] + "..."
			}

			unsupported := "-"
			if len(p.UnsupportedFeatures) > 0 {
				unsupported = fmt.Sprintf("%d features", len(p.UnsupportedFeatures))
			}

			fmt.Printf("%-20s %-30s %-40s %s\n", name, hwsku, desc, unsupported)
		}

		return nil
	},
}

var platformShowCmd = &cobra.Command{
	Use:   "show <platform>",
	Short: "Show platform details and feature support",
	Long: `Show detailed information about a specific platform.

Displays platform configuration, supported and unsupported features,
and shows which features are unsupported via dependencies.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if app.net == nil {
			return fmt.Errorf("network not initialized")
		}

		platformName := args[0]
		platforms := app.net.Platforms()
		p, ok := platforms[platformName]
		if !ok {
			return fmt.Errorf("platform %q not found", platformName)
		}

		if app.jsonOutput {
			// TODO: JSON output
			return fmt.Errorf("JSON output not yet implemented")
		}

		// Platform details
		fmt.Printf("Platform: %s\n", platformName)
		if p.Description != "" {
			fmt.Printf("Description: %s\n", p.Description)
		}
		if p.HWSKU != "" {
			fmt.Printf("HWSKU: %s\n", p.HWSKU)
		}
		if p.DeviceType != "" {
			fmt.Printf("Device Type: %s\n", p.DeviceType)
		}
		if p.PortCount > 0 {
			fmt.Printf("Port Count: %d\n", p.PortCount)
		}
		if p.DefaultSpeed != "" {
			fmt.Printf("Default Speed: %s\n", p.DefaultSpeed)
		}
		if p.Dataplane != "" {
			fmt.Printf("Dataplane: %s\n", p.Dataplane)
		}

		// Feature support
		fmt.Println("\nFeature Support:")

		// Get all known features from dependency map
		allFeatures := spec.GetAllFeatures()

		var supported []string
		var unsupportedDirect []string
		var unsupportedCascade []string

		for _, feat := range allFeatures {
			if p.SupportsFeature(feat) {
				supported = append(supported, feat)
			} else {
				// Check if it's directly unsupported or via dependency
				isDirect := false
				for _, u := range p.UnsupportedFeatures {
					if u == feat {
						isDirect = true
						break
					}
				}
				if isDirect {
					unsupportedDirect = append(unsupportedDirect, feat)
				} else {
					// Find which dependency is blocking it
					deps := spec.GetFeatureDependencies(feat)
					if len(deps) > 0 {
						unsupportedCascade = append(unsupportedCascade, 
							fmt.Sprintf("%s (requires: %s)", feat, strings.Join(deps, ", ")))
					} else {
						unsupportedDirect = append(unsupportedDirect, feat)
					}
				}
			}
		}

		if len(supported) > 0 {
			fmt.Printf("  ✓ Supported: %s\n", strings.Join(supported, ", "))
		}
		if len(unsupportedDirect) > 0 {
			fmt.Printf("  ✗ Unsupported (direct): %s\n", strings.Join(unsupportedDirect, ", "))
		}
		if len(unsupportedCascade) > 0 {
			fmt.Printf("  ✗ Unsupported (cascade):\n")
			for _, uc := range unsupportedCascade {
				fmt.Printf("      %s\n", uc)
			}
		}

		// Show what disabling each unsupported feature affects
		if len(p.UnsupportedFeatures) > 0 {
			fmt.Println("\nDependency Impact:")
			for _, feat := range p.UnsupportedFeatures {
				affected := spec.GetUnsupportedDueTo(feat)
				if len(affected) > 0 {
					fmt.Printf("  %s also disables: %s\n", feat, strings.Join(affected, ", "))
				}
			}
		}

		return nil
	},
}

func init() {
	platformCmd.AddCommand(platformListCmd)
	platformCmd.AddCommand(platformShowCmd)
}

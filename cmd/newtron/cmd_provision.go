package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/network"
)

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision device(s) from topology.json",
	Long: `Provision one or more SONiC devices from the topology specification.

The topology provisioner generates a complete CONFIG_DB for each device
offline from the topology.json and service definitions, then delivers
it atomically to the device.

Without -d: provisions ALL devices in topology.json
With -d:    provisions the specified device only
Without -x: dry-run (shows generated config summary)
With -x:    execute (deliver config + save + reload)

Examples:
  newtron -S specs provision                  # Dry-run all devices
  newtron -S specs provision -d leaf1         # Dry-run specific device
  newtron -S specs provision -x              # Execute all devices
  newtron -S specs provision -d leaf1 -xs    # Execute + save for one device`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !app.net.HasTopology() {
			return fmt.Errorf("no topology.json found in spec directory %s", app.specDir)
		}

		tp, err := network.NewTopologyProvisioner(app.net)
		if err != nil {
			return err
		}

		// Get device list
		var deviceNames []string
		if app.deviceName != "" {
			deviceNames = []string{app.deviceName}
		} else {
			deviceNames = app.net.GetTopology().DeviceNames()
		}

		if len(deviceNames) == 0 {
			fmt.Println("No devices found in topology.")
			return nil
		}

		fmt.Printf("Provisioning %d device(s) from topology.json\n\n", len(deviceNames))

		for _, name := range deviceNames {
			fmt.Printf("=== %s ===\n", bold(name))

			// Generate composite (always — for both dry-run and execute)
			composite, err := tp.GenerateDeviceComposite(name)
			if err != nil {
				fmt.Printf("  %s: %v\n\n", red("ERROR"), err)
				continue
			}

			// Show summary
			fmt.Printf("  Entries: %d\n", composite.EntryCount())
			tables := make([]string, 0, len(composite.Tables))
			for table := range composite.Tables {
				tables = append(tables, table)
			}
			sort.Strings(tables)
			for _, table := range tables {
				fmt.Printf("    %s: %d keys\n", table, len(composite.Tables[table]))
			}

			if !app.executeMode {
				fmt.Println()
				continue
			}

			// Execute: connect, deliver, save, reload
			ctx := context.Background()
			fmt.Print("  Delivering... ")

			result, err := tp.ProvisionDevice(ctx, name)
			if err != nil {
				fmt.Printf("%s: %v\n\n", red("FAILED"), err)
				continue
			}
			fmt.Printf("%s (%d entries applied)\n", green("OK"), result.Applied)

			// Save config and restart BGP container if requested.
			// We restart only the bgp container instead of a full config reload
			// because config reload is destructive (flushes CONFIG_DB, stops ALL
			// services) and breaks VPP syncd. Most CONFIG_DB changes are picked
			// up via Redis keyspace notifications, but bgpcfgd cannot change the
			// BGP ASN dynamically — it requires a container restart.
			if app.saveMode {
				dev, err := app.net.ConnectDevice(ctx, name)
				if err != nil {
					fmt.Printf("  Save: %s (could not connect: %v)\n", red("FAILED"), err)
				} else {
					fmt.Print("  Saving config... ")
					if err := dev.SaveConfig(ctx); err != nil {
						fmt.Printf("%s: %v\n", red("FAILED"), err)
					} else {
						fmt.Println(green("saved"))
					}

					fmt.Print("  Restarting BGP... ")
					if err := dev.RestartService(ctx, "bgp"); err != nil {
						fmt.Printf("%s: %v\n", red("FAILED"), err)
					} else {
						fmt.Println(green("restarted"))

						// Wait for FRR + frrcfgd to finish initial config render,
						// then apply defaults that frrcfgd doesn't support.
						time.Sleep(15 * time.Second)
						fmt.Print("  Applying FRR defaults... ")
						if err := dev.ApplyFRRDefaults(ctx); err != nil {
							fmt.Printf("%s: %v\n", red("FAILED"), err)
						} else {
							fmt.Println(green("OK"))
						}
					}
					dev.Disconnect()
				}
			}

			fmt.Println()
		}

		if !app.executeMode {
			printDryRunNotice()
		}

		return nil
	},
}

// provisionCmd is registered via rootCmd.AddCommand in main.go init().

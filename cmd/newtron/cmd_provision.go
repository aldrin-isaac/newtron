package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/util"
)

const (
	// frrReadyPollInterval is how often we check whether vtysh is responsive
	// after a BGP container restart.
	frrReadyPollInterval = 2 * time.Second

	// frrReadyTimeout is the maximum time to wait for vtysh to become
	// responsive after a BGP container restart.
	frrReadyTimeout = 30 * time.Second
)

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision device(s) from topology.json",
	Long: `Provision one or more SONiC devices from the topology specification.

The topology provisioner generates a complete CONFIG_DB for each device
offline from the topology.json and service definitions, then delivers
it atomically to the device.

Without -D: provisions ALL devices in topology.json
With -D:    provisions the specified device only
Without -x: dry-run (shows generated config summary)
With -x:    execute (deliver config + save + restart BGP)
With -x --no-save: execute without persisting to disk

Examples:
  newtron -S specs provision                      # Dry-run all devices
  newtron -S specs provision -D leaf1             # Dry-run specific device
  newtron -S specs provision -x                   # Execute all devices
  newtron -S specs provision -x --no-save         # Execute without saving`,
	RunE: func(cmd *cobra.Command, args []string) error {
		hasTopology, err := app.client.HasTopology()
		if err != nil {
			return err
		}
		if !hasTopology {
			return fmt.Errorf("no topology.json found in spec directory %s", app.rootDir)
		}

		// Get device list
		var deviceNames []string
		if app.deviceName != "" {
			deviceNames = []string{app.deviceName}
		} else {
			deviceNames, err = app.client.TopologyDeviceNames()
			if err != nil {
				return err
			}
		}

		if len(deviceNames) == 0 {
			fmt.Println("No devices found in topology.")
			return nil
		}

		fmt.Printf("Provisioning %d device(s) from topology.json\n\n", len(deviceNames))

		for _, name := range deviceNames {
			fmt.Printf("=== %s ===\n", bold(name))

			// Generate composite (always — for both dry-run and execute)
			handle, err := app.client.GenerateComposite(name)
			if err != nil {
				fmt.Printf("  %s: %v\n\n", red("ERROR"), err)
				continue
			}

			// Show summary
			fmt.Printf("  Entries: %d\n", handle.EntryCount)
			tables := make([]string, 0, len(handle.Tables))
			for table := range handle.Tables {
				tables = append(tables, table)
			}
			sort.Strings(tables)
			for _, table := range tables {
				fmt.Printf("    %s: %d keys\n", table, handle.Tables[table])
			}

			if !app.executeMode {
				fmt.Println()
				continue
			}

			// Execute: deliver, save, reload
			fmt.Print("  Delivering... ")

			result, err := app.client.DeliverComposite(name, handle.Handle, "overwrite")
			if err != nil {
				fmt.Printf("%s: %v\n\n", red("FAILED"), err)
				continue
			}
			fmt.Printf("%s (%d entries applied)\n", green("OK"), result.Applied)

			// Save config and reload all SONiC services.
			// config reload stops all daemons, flushes CONFIG_DB, re-reads
			// config_db.json, and restarts daemons. This ensures:
			// 1. bgpcfgd picks up the new ASN (RCA-019: can't change dynamically)
			// 2. vrfmgrd writes VRF_TABLE to STATE_DB (intfmgrd needs this to
			//    bind VRF-bound VLAN interfaces; the HMSET notification path
			//    only writes VRF_OBJECT_TABLE, not VRF_TABLE)
			// 3. All daemons process config from a clean startup state
			if !app.noSave {
				fmt.Print("  Saving config... ")
				if err := app.client.SaveConfig(name); err != nil {
					fmt.Printf("%s: %v\n", red("FAILED"), err)
				} else {
					fmt.Println(green("saved"))
				}

				fmt.Print("  Reloading config... ")
				if err := app.client.ConfigReload(name); err != nil {
					fmt.Printf("%s: %v\n", red("FAILED"), err)
				} else {
					fmt.Println(green("reloaded"))

					// Poll until FRR/vtysh is responsive, then apply
					// defaults that frrcfgd doesn't support.
					fmt.Print("  Waiting for FRR... ")
					if err := waitForFRR(name); err != nil {
						fmt.Printf("%s: %v\n", yellow("WARN"), err)
					} else {
						fmt.Println(green("ready"))
					}
					fmt.Print("  Applying FRR defaults... ")
					if err := app.client.ApplyFRRDefaults(name); err != nil {
						fmt.Printf("%s: %v\n", red("FAILED"), err)
					} else {
						fmt.Println(green("OK"))
					}
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

// waitForFRR polls vtysh until it responds or timeout expires.
func waitForFRR(name string) error {
	deadline := time.After(frrReadyTimeout)
	ticker := time.NewTicker(frrReadyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return fmt.Errorf("FRR did not become responsive within %s", frrReadyTimeout)
		case <-ticker.C:
			_, err := app.client.SSHCommand(name, "vtysh -c 'show version'")
			if err == nil {
				return nil
			}
			util.Logger.Debugf("waitForFRR: vtysh not ready yet: %v", err)
		}
	}
}

// provisionCmd is registered via rootCmd.AddCommand in main.go init().
